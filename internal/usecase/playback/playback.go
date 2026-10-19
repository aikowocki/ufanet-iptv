// Package playback формирует ссылку на HLS-поток канала.
package playback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// ChannelRepository возвращает канал по id.
type ChannelRepository interface {
	// GetChannel возвращает канал или domain.ErrNotFound.
	GetChannel(ctx context.Context, id int64) (domain.Channel, error)
	// HasChannelAccess сообщает, есть ли у пользователя действующая подписка на канал.
	HasChannelAccess(ctx context.Context, userID, channelID int64) (bool, error)
}

// Signer подписывает ссылку на поток для пользователя.
type Signer interface {
	Sign(rawURL string, uid int64, ttl time.Duration) (string, error)
}

// Service строит подписанные ссылки на потоки.
type Service struct {
	channels ChannelRepository
	signer   Signer
	baseURL  string
	ttl      time.Duration
}

// New создаёт Service. baseURL — адрес стрим-сервера без завершающего слэша,
// ttl — срок действия выданной ссылки.
func New(channels ChannelRepository, signer Signer, baseURL string, ttl time.Duration) *Service {
	return &Service{channels: channels, signer: signer, baseURL: strings.TrimRight(baseURL, "/"), ttl: ttl}
}

// Link возвращает подписанную ссылку на поток, привязанную к пользователю.
// Выключенный канал считается несуществующим (domain.ErrNotFound); существующий канал
// без действующей подписки даёт domain.ErrForbidden.
func (s *Service) Link(ctx context.Context, user domain.User, channelID int64) (string, error) {
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
	ok, err := s.channels.HasChannelAccess(ctx, user.ID, ch.ID)
	if err != nil {
		return "", fmt.Errorf("check access: %w", err)
	}
	if !ok {
		return "", domain.ErrForbidden
	}
	link, err := s.signer.Sign(s.baseURL+"/"+strings.TrimLeft(ch.StreamPath, "/"), user.ID, s.ttl)
	if err != nil {
		return "", fmt.Errorf("sign link: %w", err)
	}
	return link, nil
}
