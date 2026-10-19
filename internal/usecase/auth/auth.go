// Package auth проверяет логин и пароль абонента.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// UserRepository возвращает пользователя по логину или domain.ErrNotFound.
type UserRepository interface {
	GetUserByLogin(ctx context.Context, login string) (domain.User, error)
}

// PasswordVerifier сравнивает пароль с сохранённым хэшем. При перегрузке
// реализация возвращает ошибку, оборачивающую domain.ErrBusy.
type PasswordVerifier interface {
	Verify(ctx context.Context, password, hash string) (bool, error)
}

// Service выполняет аутентификацию.
type Service struct {
	users UserRepository
	pw    PasswordVerifier
	// dummyHash проверяется, когда пользователя нет, чтобы время ответа
	// не выдавало существование логина.
	dummyHash string
}

// New создаёт Service. dummyHash — корректный хэш любого пароля,
// который не должен совпадать ни с чем осмысленным (например, хэш случайной строки).
func New(users UserRepository, pw PasswordVerifier, dummyHash string) *Service {
	return &Service{users: users, pw: pw, dummyHash: dummyHash}
}

// Authenticate возвращает пользователя при верных учётных данных.
// Для любой причины отказа (нет пользователя, неверный пароль, пользователь выключен)
// возвращается domain.ErrInvalidCredentials. Пароль проверяется и для выключенного
// пользователя, поэтому время ответа не зависит от причины.
func (s *Service) Authenticate(ctx context.Context, login, password string) (domain.User, error) {
	user, err := s.users.GetUserByLogin(ctx, login)
	hash := user.PasswordHash
	found := true
	switch {
	case errors.Is(err, domain.ErrNotFound):
		hash, found = s.dummyHash, false
	case err != nil:
		return domain.User{}, fmt.Errorf("get user: %w", err)
	}

	ok, err := s.pw.Verify(ctx, password, hash)
	if err != nil {
		return domain.User{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok || !found || !user.IsActive {
		return domain.User{}, domain.ErrInvalidCredentials
	}
	return user, nil
}
