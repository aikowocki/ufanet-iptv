package postgres

import (
	"context"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// Интеграционный тест: пропускается без TEST_DATABASE_URL.
func TestListUserSubscriptions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewSubscriptionRepository(NewExecutor(pool))

	for _, q := range []string{
		`INSERT INTO users (login, password_hash) VALUES ('u','h'),('other','h')`,
		`INSERT INTO packages (name, is_active) VALUES ('a-forever',true),('b-expired',true),('c-future',true),
			('d-sub-off',true),('e-pkg-off',false)`,
		`INSERT INTO subscriptions (user_id, package_id, is_active, expires_at)
			SELECT u.id, p.id, s.active, s.exp FROM users u, packages p,
			(VALUES ('a-forever', true,  NULL::timestamptz),
			        ('b-expired', true,  now() - interval '1 minute'),
			        ('c-future',  true,  now() + interval '1 hour'),
			        ('d-sub-off', false, NULL),
			        ('e-pkg-off', true,  NULL)) AS s(pkg, active, exp)
			WHERE u.login = 'u' AND p.name = s.pkg`,
		`INSERT INTO subscriptions (user_id, package_id) SELECT u.id, p.id FROM users u, packages p
			WHERE u.login = 'other' AND p.name = 'a-forever'`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var uid int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'u'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}

	list, err := repo.ListUserSubscriptions(ctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.SubscriptionStatus{
		domain.SubscriptionActive, domain.SubscriptionExpired, domain.SubscriptionActive,
		domain.SubscriptionDisabled, domain.SubscriptionDisabled,
	}
	if len(list) != len(want) {
		t.Fatalf("got %d subscriptions, want %d: %+v", len(list), len(want), list)
	}
	for i, s := range list {
		if s.Status != want[i] {
			t.Errorf("%s: status = %q, want %q", s.PackageName, s.Status, want[i])
		}
	}
	if list[0].ExpiresAt != nil || list[2].ExpiresAt == nil {
		t.Errorf("expires_at: forever = %v, future = %v", list[0].ExpiresAt, list[2].ExpiresAt)
	}
}
