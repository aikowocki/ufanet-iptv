package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/infra/cache"
)

// Authenticator проверяет учётные данные; его реализует Service.
type Authenticator interface {
	Authenticate(ctx context.Context, login, password string) (domain.User, error)
}

// Cached кэширует успешные проверки пароля: argon2id занимает десятки миллисекунд и
// около 19 МиБ на каждый запрос, а Basic Auth передаёт пароль в каждом запросе.
//
//   - Ключ кэша: HMAC-SHA256 от пары логин и пароль со случайным секретом процесса.
//     Пароль в памяти в открытом виде не хранится, а ключ нельзя проверить перебором
//     без секрета.
//   - Кэшируются только успехи. Отказ (неверный пароль, неизвестный логин, выключенный
//     пользователь) всегда идёт по полному пути, поэтому память не растёт от перебора
//     и выравнивание времени ответа через dummyHash остаётся в силе.
//   - Одновременные промахи по одному ключу схлопываются через singleflight.
//   - Устаревание ограничено TTL; InvalidateUser сбрасывает записи пользователя сразу
//     (в пределах процесса).
type Cached struct {
	next   Authenticator
	ttl    time.Duration
	secret []byte
	cache  *cache.TTL[string, domain.User]
	flight singleflight.Group
	// gen растёт при каждой инвалидации: результат проверки, начатой до неё,
	// в кэш не попадает (иначе устаревшие данные пережили бы сброс).
	gen atomic.Uint64
}

// NewCached оборачивает next кэшем с временем жизни записи ttl.
// now == nil означает time.Now.
func NewCached(next Authenticator, ttl time.Duration, now func() time.Time) (*Cached, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("auth cache: ttl must be positive, got %s", ttl)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("auth cache: secret: %w", err)
	}
	return &Cached{next: next, ttl: ttl, secret: secret, cache: cache.NewTTL[string, domain.User](ttl, now)}, nil
}

func (c *Cached) key(login, password string) string {
	m := hmac.New(sha256.New, c.secret)
	// Разделитель однозначен: логин не содержит NUL, иначе пары ("a\x00b","c") и ("a","b\x00c") совпали бы.
	m.Write([]byte(login))
	m.Write([]byte{0})
	m.Write([]byte(password))
	return string(m.Sum(nil))
}

// Authenticate возвращает пользователя из кэша либо проверяет учётные данные через next.
func (c *Cached) Authenticate(ctx context.Context, login, password string) (domain.User, error) {
	key := c.key(login, password)
	if u, ok := c.cache.Get(key); ok {
		return u, nil
	}
	// Работа не должна обрываться, если ушёл клиент, чей запрос стал «лидером»:
	// результат нужен остальным. Каждый ожидающий при этом следит за своим ctx.
	ch := c.flight.DoChan(key, func() (any, error) {
		gen := c.gen.Load()
		u, err := c.next.Authenticate(context.WithoutCancel(ctx), login, password)
		if err == nil && gen == c.gen.Load() {
			c.cache.Set(key, u, c.ttl)
		}
		return u, err
	})
	select {
	case <-ctx.Done():
		return domain.User{}, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return domain.User{}, res.Err
		}
		return res.Val.(domain.User), nil
	}
}

// InvalidateUser сбрасывает закэшированные проверки пользователя: после смены пароля,
// выключения или смены роли.
func (c *Cached) InvalidateUser(id int64) {
	c.gen.Add(1)
	c.cache.DeleteFunc(func(_ string, u domain.User) bool { return u.ID == id })
}

// InvalidateAll сбрасывает все закэшированные проверки: после потери событий инвалидации.
func (c *Cached) InvalidateAll() {
	c.gen.Add(1)
	c.cache.Clear()
}
