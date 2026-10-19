package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier — подмножество методов pgxpool.Pool и pgx.Tx, нужное репозиториям.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Executor выполняет запросы в транзакции из ctx, а если её нет, через пул.
// Сам удовлетворяет Querier, поэтому репозитории принимают его как обычное соединение.
type Executor struct{ pool Querier }

// NewExecutor создаёт Executor поверх пула.
func NewExecutor(pool Querier) *Executor { return &Executor{pool: pool} }

func (e *Executor) conn(ctx context.Context) Querier {
	if tx, ok := txFromContext(ctx); ok {
		return tx
	}
	return e.pool
}

// Query выполняет запрос, возвращающий строки.
func (e *Executor) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return e.conn(ctx).Query(ctx, sql, args...)
}

// QueryRow выполняет запрос, возвращающий одну строку.
func (e *Executor) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return e.conn(ctx).QueryRow(ctx, sql, args...)
}

// Exec выполняет команду без результата.
func (e *Executor) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return e.conn(ctx).Exec(ctx, sql, args...)
}
