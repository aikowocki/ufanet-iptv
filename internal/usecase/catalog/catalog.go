// Package catalog отдаёт список каналов.
package catalog

import (
	"context"
	"fmt"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// ChannelRepository читает каналы из хранилища.
type ChannelRepository interface {
	// ListActiveChannels возвращает включённые каналы, отсортированные по номеру.
	ListActiveChannels(ctx context.Context) ([]domain.Channel, error)
}

// Service — каталог каналов.
type Service struct{ channels ChannelRepository }

// New создаёт Service.
func New(channels ChannelRepository) *Service { return &Service{channels: channels} }

// List возвращает каналы, доступные пользователю. Пока подписок нет,
// доступны все включённые каналы.
func (s *Service) List(ctx context.Context, _ domain.User) ([]domain.Channel, error) {
	list, err := s.channels.ListActiveChannels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list channels: %w", err)
	}
	return list, nil
}
