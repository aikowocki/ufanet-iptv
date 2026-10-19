package rest_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/infra/passhash"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/middleware"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/auth"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/catalog"
)

type benchUsers struct{ user domain.User }

func (u benchUsers) GetUserByLogin(context.Context, string) (domain.User, error) { return u.user, nil }

type benchChannels struct{ list []domain.ChannelAccess }

func (c benchChannels) ListChannelsForUser(context.Context, int64) ([]domain.ChannelAccess, error) {
	return c.list, nil
}

// BenchmarkChannels гоняет GET /api/v1/channels через настоящий роутер, middleware и argon2id.
// БД подменена: измеряется накладная стоимость самого сервиса, без сети и SQL.
func BenchmarkChannels(b *testing.B) { runChannelsBench(b, false) }

// BenchmarkChannelsCachedAuth - то же с кэшем успешных проверок пароля.
func BenchmarkChannelsCachedAuth(b *testing.B) { runChannelsBench(b, true) }

func runChannelsBench(b *testing.B, cached bool) {
	ctx := context.Background()
	hasher := passhash.New(4, time.Minute)
	enc, err := hasher.Hash(ctx, "alice123")
	if err != nil {
		b.Fatal(err)
	}
	list := make([]domain.ChannelAccess, 30)
	for i := range list {
		list[i] = domain.ChannelAccess{
			Channel:   domain.Channel{ID: int64(i + 1), Number: i + 1, Name: "Канал", IsActive: true},
			Available: i%2 == 0,
		}
	}
	users := benchUsers{domain.User{ID: 1, Login: "alice", PasswordHash: enc, IsActive: true}}
	var authn middleware.Authenticator = auth.New(users, hasher, enc)
	if cached {
		c, err := auth.NewCached(authn.(auth.Authenticator), time.Minute, nil)
		if err != nil {
			b.Fatal(err)
		}
		authn = c
	}
	h := rest.NewRouter(slog.New(slog.DiscardHandler), nil, rest.Deps{
		Auth:     authn,
		Channels: catalog.New(benchChannels{list}),
	})
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/channels", nil)
			req.SetBasicAuth("alice", "alice123")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				b.Errorf("status %d", rec.Code)
				return
			}
		}
	})
}
