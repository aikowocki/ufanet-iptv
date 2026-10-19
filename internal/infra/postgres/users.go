package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// UserRepository читает пользователей. Все запросы параметризованы.
type UserRepository struct{ db Querier }

// NewUserRepository создаёт UserRepository.
func NewUserRepository(db Querier) *UserRepository { return &UserRepository{db: db} }

// GetUserByLogin возвращает пользователя или domain.ErrNotFound.
func (r *UserRepository) GetUserByLogin(ctx context.Context, login string) (domain.User, error) {
	var u domain.User
	err := r.db.QueryRow(ctx,
		`SELECT id, login, password_hash, is_active, is_admin FROM users WHERE login = $1`, login,
	).Scan(&u.ID, &u.Login, &u.PasswordHash, &u.IsActive, &u.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}
