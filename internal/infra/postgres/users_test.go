package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// Интеграционный тест: пропускается без TEST_DATABASE_URL.
func TestUserRepository(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewUserRepository(NewExecutor(pool))

	if _, err := pool.Exec(ctx, `INSERT INTO users (login, password_hash, is_active) VALUES ('u1','h',true),('u2','h',false)`); err != nil {
		t.Fatal(err)
	}

	u, err := repo.GetUserByLogin(ctx, "u1")
	if err != nil || u.Login != "u1" || !u.IsActive {
		t.Fatalf("GetUserByLogin = %+v, %v", u, err)
	}
	u, err = repo.GetUserByLogin(ctx, "u2")
	if err != nil || u.IsActive {
		t.Fatalf("inactive user = %+v, %v", u, err)
	}
	if _, err := repo.GetUserByLogin(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user err = %v, want ErrNotFound", err)
	}
	// Инъекция воспринимается как обычный логин.
	if _, err := repo.GetUserByLogin(ctx, "u1' OR '1'='1"); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("injection err = %v, want ErrNotFound", err)
	}
}
