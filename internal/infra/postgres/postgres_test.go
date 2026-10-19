package postgres

import (
	"context"
	"io"
	"log/slog"
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

// Интеграционный тест (нужен TEST_DATABASE_URL): миграции и seed в отдельной схеме,
// повторный запуск ничего не дублирует.
func TestMigrateAndSeed(t *testing.T) {
	dsn := newTestSchemaDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

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
