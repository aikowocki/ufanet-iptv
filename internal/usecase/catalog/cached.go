package catalog

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/infra/cache"
)

// Lister возвращает каналы пользователя; его реализует Service.
type Lister interface {
	List(ctx context.Context, user domain.User) ([]domain.ChannelAccess, error)
}

// ExpiryFinder сообщает, через сколько истечёт ближайшая действующая подписка пользователя
// с ограниченным сроком (ok == false, если таких нет).
type ExpiryFinder interface {
	NextSubscriptionExpiry(ctx context.Context, userID int64) (d time.Duration, ok bool, err error)
}

// Cached кэширует список каналов пользователя в памяти процесса.
//
//   - Запись живёт не дольше ttl и не дольше ближайшего expires_at подписки пользователя,
//     поэтому истёкшая подписка закрывает каналы вовремя без участия администратора.
//   - Срок подписки читается до списка каналов: если подписка истечёт между двумя запросами,
//     запись окажется просроченной сразу и не сохранится.
//   - Изменения через admin API сбрасывают кэш целиком (InvalidateCatalog). Изменения,
//     сделанные другим экземпляром сервиса, видны не позже чем через ttl.
//   - Одновременные промахи по одному пользователю схлопываются через singleflight.
//
// Возвращённый срез общий для всех читателей: изменять его нельзя.
type Cached struct {
	next   Lister
	expiry ExpiryFinder
	ttl    time.Duration
	cache  *cache.TTL[int64, []domain.ChannelAccess]
	flight singleflight.Group
	gen    atomic.Uint64
}

// NewCached оборачивает next кэшем со временем жизни записи ttl. now == nil означает time.Now.
func NewCached(next Lister, expiry ExpiryFinder, ttl time.Duration, now func() time.Time) (*Cached, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("catalog cache: ttl must be positive, got %s", ttl)
	}
	return &Cached{
		next: next, expiry: expiry, ttl: ttl,
		cache: cache.NewTTL[int64, []domain.ChannelAccess](ttl, now),
	}, nil
}

// List возвращает каналы из кэша либо читает их через next.
func (c *Cached) List(ctx context.Context, user domain.User) ([]domain.ChannelAccess, error) {
	if list, ok := c.cache.Get(user.ID); ok {
		return list, nil
	}
	ch := c.flight.DoChan(strconv.FormatInt(user.ID, 10), func() (any, error) {
		ctx := context.WithoutCancel(ctx) // результат нужен и остальным ожидающим
		gen := c.gen.Load()
		ttl := c.ttl
		d, bounded, err := c.expiry.NextSubscriptionExpiry(ctx, user.ID)
		if err != nil {
			return nil, fmt.Errorf("next subscription expiry: %w", err)
		}
		if bounded && d < ttl {
			ttl = d
		}
		list, err := c.next.List(ctx, user)
		if err != nil {
			return nil, err
		}
		if gen == c.gen.Load() {
			c.cache.Set(user.ID, list, ttl)
		}
		return list, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]domain.ChannelAccess), nil
	}
}

// InvalidateCatalog сбрасывает кэш всех пользователей: после изменения каналов, пакетов,
// их состава или подписок.
func (c *Cached) InvalidateCatalog() {
	c.gen.Add(1)
	c.cache.Clear()
}
