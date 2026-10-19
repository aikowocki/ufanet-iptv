package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// stubTx считает Commit и Rollback.
type stubTx struct {
	pgx.Tx
	commits, rollbacks int
	commitErr          error
}

func (s *stubTx) Commit(context.Context) error   { s.commits++; return s.commitErr }
func (s *stubTx) Rollback(context.Context) error { s.rollbacks++; return nil }

// stubDB выдаёт заранее подготовленные транзакции.
type stubDB struct {
	txs      []*stubTx
	beginErr error
}

func (d *stubDB) Begin(context.Context) (pgx.Tx, error) {
	if d.beginErr != nil {
		return nil, d.beginErr
	}
	tx := &stubTx{}
	d.txs = append(d.txs, tx)
	return tx, nil
}

func TestTxManagerCommit(t *testing.T) {
	db := &stubDB{}
	err := NewTxManager(db).Do(context.Background(), func(ctx context.Context) error {
		if _, ok := txFromContext(ctx); !ok {
			t.Error("transaction is not in ctx")
		}
		return nil
	})
	if err != nil || len(db.txs) != 1 || db.txs[0].commits != 1 {
		t.Fatalf("err=%v txs=%d", err, len(db.txs))
	}
}

func TestTxManagerRollbackOnError(t *testing.T) {
	db := &stubDB{}
	boom := errors.New("boom")
	err := NewTxManager(db).Do(context.Background(), func(context.Context) error { return boom })
	if !errors.Is(err, boom) || db.txs[0].commits != 0 || db.txs[0].rollbacks != 1 {
		t.Fatalf("err=%v commits=%d rollbacks=%d", err, db.txs[0].commits, db.txs[0].rollbacks)
	}
}

func TestTxManagerRollbackOnPanic(t *testing.T) {
	db := &stubDB{}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_ = NewTxManager(db).Do(context.Background(), func(context.Context) error { panic("oops") })
	}()
	if db.txs[0].commits != 0 || db.txs[0].rollbacks != 1 {
		t.Errorf("commits=%d rollbacks=%d", db.txs[0].commits, db.txs[0].rollbacks)
	}
}

func TestTxManagerNestedReusesTx(t *testing.T) {
	db := &stubDB{}
	m := NewTxManager(db)
	err := m.Do(context.Background(), func(ctx context.Context) error {
		return m.Do(ctx, func(context.Context) error { return nil })
	})
	if err != nil || len(db.txs) != 1 || db.txs[0].commits != 1 {
		t.Fatalf("err=%v txs=%d", err, len(db.txs))
	}
}

func TestTxManagerRetriesSerializationFailure(t *testing.T) {
	db := &stubDB{}
	calls := 0
	err := NewTxManager(db).Do(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return &pgconn.PgError{Code: codeSerializationFailure}
		}
		return nil
	})
	if err != nil || calls != 3 || len(db.txs) != 3 || db.txs[2].commits != 1 {
		t.Fatalf("err=%v calls=%d txs=%d", err, calls, len(db.txs))
	}
}

func TestTxManagerDoesNotRetryOtherErrors(t *testing.T) {
	db := &stubDB{}
	calls := 0
	err := NewTxManager(db).Do(context.Background(), func(context.Context) error {
		calls++
		return &pgconn.PgError{Code: "23505"} // unique_violation
	})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want one call and an error", err, calls)
	}
}

func TestTxManagerGivesUpAfterMaxAttempts(t *testing.T) {
	db := &stubDB{}
	calls := 0
	err := NewTxManager(db).Do(context.Background(), func(context.Context) error {
		calls++
		return &pgconn.PgError{Code: codeDeadlockDetected}
	})
	if err == nil || calls != txMaxAttempts {
		t.Fatalf("err=%v calls=%d, want %d", err, calls, txMaxAttempts)
	}
}

func TestTxManagerCommitError(t *testing.T) {
	boom := errors.New("commit failed")
	db := &commitFailDB{err: boom}
	err := NewTxManager(db).Do(context.Background(), func(context.Context) error { return nil })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want commit error", err)
	}
}

type commitFailDB struct{ err error }

func (d *commitFailDB) Begin(context.Context) (pgx.Tx, error) {
	return &stubTx{commitErr: d.err}, nil
}

func TestTxManagerBadInput(t *testing.T) {
	ctx := context.Background()
	if err := NewTxManager(&stubDB{}).Do(ctx, nil); err == nil {
		t.Error("nil callback: expected error")
	}
	var nilM *TxManager
	if err := nilM.Do(ctx, func(context.Context) error { return nil }); err == nil {
		t.Error("nil manager: expected error")
	}
	if err := NewTxManager(&stubDB{beginErr: errors.New("down")}).Do(ctx, func(context.Context) error { return nil }); err == nil {
		t.Error("begin error: expected error")
	}
}

func TestTxManagerStopsRetryOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := NewTxManager(&stubDB{}).Do(ctx, func(context.Context) error {
		calls++
		cancel()
		return &pgconn.PgError{Code: codeSerializationFailure}
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

// Интеграционный тест: откат не оставляет записей, коммит оставляет.
// Пропускается без TEST_DATABASE_URL.
func TestTxManagerIntegration(t *testing.T) {
	pool := newTestPool(t)
	exec := NewExecutor(pool)
	m := NewTxManager(pool)
	ctx := context.Background()
	insert := func(ctx context.Context, login string) error {
		_, err := exec.Exec(ctx, `INSERT INTO users (login, password_hash) VALUES ($1, 'h')`, login)
		return err
	}
	exists := func(login string) bool {
		_, err := NewUserRepository(exec).GetUserByLogin(ctx, login)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}

	boom := errors.New("boom")
	err := m.Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, "rolled_back"); err != nil {
			return err
		}
		if err := insert(ctx, "also_rolled_back"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || exists("rolled_back") || exists("also_rolled_back") {
		t.Fatalf("rollback failed: err=%v", err)
	}

	err = m.Do(ctx, func(ctx context.Context) error {
		if err := insert(ctx, "committed_1"); err != nil {
			return err
		}
		return insert(ctx, "committed_2")
	})
	if err != nil || !exists("committed_1") || !exists("committed_2") {
		t.Fatalf("commit failed: err=%v", err)
	}
}
