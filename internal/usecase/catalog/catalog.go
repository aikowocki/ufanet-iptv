// Package catalog отдаёт список каналов.
package catalog

import (
	"context"
	"fmt"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// ChannelRepository читает каналы из хранилища.
type ChannelRepository interface {
	// ListChannelsForUser возвращает все включённые каналы, отсортированные по номеру,
	// с признаком доступности по подписке пользователя.
	ListChannelsForUser(ctx context.Context, userID int64) ([]domain.ChannelAccess, error)
}

// Service — каталог каналов.
type Service struct{ channels ChannelRepository }

// New создаёт Service.
func New(channels ChannelRepository) *Service { return &Service{channels: channels} }

// List возвращает все включённые каналы; на недоступных (нет подписки или она истекла)
// стоит Available = false, чтобы клиент мог предложить оформить подписку.
func (s *Service) List(ctx context.Context, user domain.User) ([]domain.ChannelAccess, error) {
	list, err := s.channels.ListChannelsForUser(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return list, nil
}
