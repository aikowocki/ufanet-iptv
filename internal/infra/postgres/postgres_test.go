package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrateURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "postgres://u@h:5432/db?sslmode=disable", want: "pgx5://u@h:5432/db?sslmode=disable"},
		{in: "postgresql://u@h/db", want: "pgx5://u@h/db"},
		{in: "pgx5://u@h/db", want: "pgx5://u@h/db"},
		{in: "mysql://u@h/db", wantErr: true},
	}
	for _, tt := range tests {
		got, err := migrateURL(tt.in)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("migrateURL(%q) = %q, %v; want %q, err=%v", tt.in, got, err, tt.want, tt.wantErr)
		}
	}
}

type fakeHasher struct{ calls int }

func (f *fakeHasher) Hash(_ context.Context, password string) (string, error) {
	f.calls++
	return "hash:" + password, nil
}

// Интеграционный тест: миграции и seed в отдельной схеме, повторный запуск ничего не дублирует.
// Запускается только при заданном TEST_DATABASE_URL.
func TestMigrateAndSeed(t *testing.T) {
	baseURL := os.Getenv("TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	admin, err := NewPool(ctx, log, baseURL, 2, 10*time.Second)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer admin.Close()

	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	})

	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	dsn := u.String()

	// Повторное применение миграций безопасно.
	for range 2 {
		if err := Migrate(log, dsn); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	h := &fakeHasher{}
	for range 2 {
		if err := Seed(ctx, log, pool, h); err != nil {
			t.Fatalf("Seed: %v", err)
		}
	}
	if h.calls != len(seedUsers) {
		t.Errorf("hash calls = %d, want %d (повторный seed не должен хэшировать)", h.calls, len(seedUsers))
	}

	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}
	// Администратор приходит из миграции, остальные из сидера.
	if got := count("users"); got != len(seedUsers)+1 {
		t.Errorf("users = %d, want %d", got, len(seedUsers)+1)
	}
	if got := count("channels"); got != len(seedChannels) || got < 5 {
		t.Errorf("channels = %d, want %d (>= 5)", got, len(seedChannels))
	}

	// Админ создан миграцией (до сидера), с настоящим argon2id-хэшем.
	var isAdmin bool
	var hash string
	if err := pool.QueryRow(ctx, `SELECT is_admin, password_hash FROM users WHERE login = 'admin'`).Scan(&isAdmin, &hash); err != nil {
		t.Fatal(err)
	}
	if !isAdmin || !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("admin = %v, hash = %q", isAdmin, hash)
	}
}
