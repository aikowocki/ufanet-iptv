package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// recorder запоминает, что его вызывали. Служит и пулом, и транзакцией.
type recorder struct{ calls int }

func (r *recorder) Query(context.Context, string, ...any) (pgx.Rows, error) {
	r.calls++
	return nil, nil
}

func (r *recorder) QueryRow(context.Context, string, ...any) pgx.Row {
	r.calls++
	return nil
}

func (r *recorder) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	r.calls++
	return pgconn.CommandTag{}, nil
}

type fakeTx struct {
	pgx.Tx // остальные методы не нужны, вызов приведёт к панике
	recorder
}

func (f *fakeTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return f.recorder.Query(ctx, sql, args...)
}

func (f *fakeTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return f.recorder.QueryRow(ctx, sql, args...)
}

func (f *fakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return f.recorder.Exec(ctx, sql, args...)
}

func TestExecutorChoosesConnection(t *testing.T) {
	ops := map[string]func(*Executor, context.Context){
		"Query":    func(e *Executor, ctx context.Context) { _, _ = e.Query(ctx, "select 1") },
		"QueryRow": func(e *Executor, ctx context.Context) { _ = e.QueryRow(ctx, "select 1") },
		"Exec":     func(e *Executor, ctx context.Context) { _, _ = e.Exec(ctx, "select 1") },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			pool, tx := &recorder{}, &fakeTx{}
			exec := NewExecutor(pool)

			op(exec, context.Background())
			if pool.calls != 1 || tx.calls != 0 {
				t.Errorf("without tx: pool=%d tx=%d, want pool=1 tx=0", pool.calls, tx.calls)
			}

			op(exec, withTx(context.Background(), tx))
			if pool.calls != 1 || tx.calls != 1 {
				t.Errorf("with tx: pool=%d tx=%d, want pool=1 tx=1", pool.calls, tx.calls)
			}
		})
	}
}
