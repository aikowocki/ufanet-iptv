package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Коды Postgres, при которых транзакцию можно безопасно повторить целиком.
const (
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
)

const (
	txMaxAttempts  = 3
	txRetryBackoff = 10 * time.Millisecond
)

// TxBeginner открывает транзакции. Удовлетворяется *pgxpool.Pool.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// txKey хранит активную транзакцию в context.
type txKey struct{}

// TxManager объединяет несколько операций репозиториев в одну транзакцию БД.
//
// Транзакция передаётся неявно, через ctx: сигнатуры методов репозиториев не зависят
// от того, вызваны они внутри транзакции или вне её. Границу транзакции задаёт
// usecase, репозитории о ней не знают.
type TxManager struct {
	db TxBeginner
}

// NewTxManager создаёт TxManager поверх пула.
func NewTxManager(db TxBeginner) *TxManager {
	return &TxManager{db: db}
}

// Do выполняет fn внутри одной транзакции: Commit при успехе, Rollback при ошибке
// или панике. Вложенный вызов использует уже активную транзакцию (без savepoint).
//
// При сбое сериализации или deadlock транзакция повторяется целиком, поэтому fn
// должна быть безопасна для повторного вызова: не менять состояние вне БД.
func (m *TxManager) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return errors.New("run transaction: callback is nil")
	}
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}
	if m == nil || m.db == nil {
		return errors.New("run transaction: database is not configured")
	}

	return withRetry(ctx, isTxRetryable, func() error {
		tx, err := m.db.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}
		// Rollback после успешного Commit безвреден (pgx.ErrTxClosed). Контекст без отмены,
		// чтобы откат выполнился, даже если ctx запроса уже отменён.
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

		if err := fn(withTx(ctx, tx)); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit transaction: %w", err)
		}
		return nil
	})
}

func withTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}

// isTxRetryable сообщает, что ошибка временная и транзакцию стоит повторить.
func isTxRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == codeSerializationFailure || pgErr.Code == codeDeadlockDetected
}

// withRetry вызывает op до txMaxAttempts раз, пока ошибка удовлетворяет retryable.
// Между попытками ждёт с растущей паузой; ожидание прерывается отменой ctx.
func withRetry(ctx context.Context, retryable func(error) bool, op func() error) error {
	var err error
	for attempt := 1; attempt <= txMaxAttempts; attempt++ {
		if err = op(); err == nil || !retryable(err) || attempt == txMaxAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(txRetryBackoff * time.Duration(attempt)):
		}
	}
	return err
}
