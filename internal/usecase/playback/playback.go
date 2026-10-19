// Package playback формирует ссылку на HLS-поток канала.
package playback

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// ChannelRepository возвращает канал по id.
type ChannelRepository interface {
	// GetChannel возвращает канал или domain.ErrNotFound.
	GetChannel(ctx context.Context, id int64) (domain.Channel, error)
}

// Service строит ссылки на потоки.
type Service struct {
	channels ChannelRepository
	baseURL  string
}

// New создаёт Service. baseURL — адрес стрим-сервера без завершающего слэша.
func New(channels ChannelRepository, baseURL string) *Service {
	return &Service{channels: channels, baseURL: strings.TrimRight(baseURL, "/")}
}

// Link возвращает ссылку на поток. Выключенный канал считается несуществующим.
func (s *Service) Link(ctx context.Context, _ domain.User, channelID int64) (string, error) {
	ch, err := s.channels.GetChannel(ctx, channelID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("get channel: %w", err)
	}
	if !ch.IsActive {
		return "", domain.ErrNotFound
	}
	return s.baseURL + "/" + strings.TrimLeft(ch.StreamPath, "/"), nil
}
