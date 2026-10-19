// Package subscription отдаёт подписки пользователя.
package subscription

import (
	"context"
	"fmt"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// Repository читает подписки из хранилища.
type Repository interface {
	// ListUserSubscriptions возвращает все подписки пользователя со статусом на текущий момент.
	ListUserSubscriptions(ctx context.Context, userID int64) ([]domain.Subscription, error)
}

// Service — подписки пользователя.
type Service struct{ repo Repository }

// New создаёт Service.
func New(repo Repository) *Service { return &Service{repo: repo} }

// List возвращает подписки пользователя: действующие, истёкшие и выключенные.
func (s *Service) List(ctx context.Context, user domain.User) ([]domain.Subscription, error) {
	list, err := s.repo.ListUserSubscriptions(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	return list, nil
}
