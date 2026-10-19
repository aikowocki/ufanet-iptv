package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestSchemaDSN создаёт пустую схему в базе из TEST_DATABASE_URL и возвращает DSN
// с search_path на неё. Схема удаляется по завершении теста, поэтому тесты изолированы
// друг от друга и не трогают данные в public.
// Без TEST_DATABASE_URL тест пропускается, а в CI (переменная CI) падает.
func newTestSchemaDSN(t *testing.T) string {
	t.Helper()
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("TEST_DATABASE_URL не задан в CI")
		}
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	admin, err := NewPool(ctx, log, baseURL, 2, 10*time.Second)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(admin.Close)

	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// newTestPool создаёт отдельную схему с применёнными миграциями (без seed).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := newTestSchemaDSN(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Migrate(log, dsn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
