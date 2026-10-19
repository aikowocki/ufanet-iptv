package postgres

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// Интеграционный тест SQL-логики подписок: бессрочная, срок истёк, срок идёт,
// выключенная подписка, выключенный пакет, выключенный канал, чужая подписка.
func TestChannelAccess(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewChannelRepository(NewExecutor(pool))

	mustExec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	mustExec(`INSERT INTO users (login, password_hash) VALUES ('u','h'),('other','h')`)
	mustExec(`INSERT INTO channels (number, name, stream_path, is_active) VALUES
		(1,'forever','/1',true),(2,'expired','/2',true),(3,'future','/3',true),
		(4,'sub-off','/4',true),(5,'pkg-off','/5',true),(6,'ch-off','/6',false),(7,'nobody','/7',true)`)
	mustExec(`INSERT INTO packages (name, is_active) VALUES
		('p1',true),('p2',true),('p3',true),('p4',true),('p5',false),('p6',true)`)
	// Каналы 1..6 лежат в пакетах p1..p6 соответственно (канал 5 в выключенном p5).
	mustExec(`INSERT INTO package_channels (package_id, channel_id)
		SELECT p.id, c.id FROM packages p JOIN channels c ON c.number = substr(p.name, 2)::int`)
	mustExec(`INSERT INTO subscriptions (user_id, package_id, is_active, expires_at)
		SELECT u.id, p.id, s.active, s.exp FROM users u, packages p,
		(VALUES ('p1', true,  NULL::timestamptz),
		        ('p2', true,  now() - interval '1 minute'),
		        ('p3', true,  now() + interval '1 hour'),
		        ('p4', false, NULL),
		        ('p5', true,  NULL),
		        ('p6', true,  NULL)) AS s(pkg, active, exp)
		WHERE u.login = 'u' AND p.name = s.pkg`)
	mustExec(`INSERT INTO subscriptions (user_id, package_id)
		SELECT u.id, p.id FROM users u, packages p WHERE u.login = 'other' AND p.name = 'p4'`)

	userID := func(login string) int64 {
		t.Helper()
		var id int64
		if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE login = $1`, login).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	u, other := userID("u"), userID("other")

	// Список содержит все включённые каналы (кроме выключенного 6), доступные помечены.
	avail := func(userID int64) (all, available []int) {
		t.Helper()
		list, err := repo.ListChannelsForUser(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range list {
			all = append(all, c.Number)
			if c.Available {
				available = append(available, c.Number)
			}
		}
		return all, available
	}
	all, got := avail(u)
	if !slices.Equal(all, []int{1, 2, 3, 4, 5, 7}) || !slices.Equal(got, []int{1, 3}) {
		t.Errorf("user u: all = %v, available = %v; want [1 2 3 4 5 7] / [1 3]", all, got)
	}
	if all, got = avail(other); len(all) != 6 || !slices.Equal(got, []int{4}) {
		t.Errorf("other user: all = %v, available = %v; want 6 channels, only 4 available", all, got)
	}

	for number, want := range map[int]bool{1: true, 2: false, 3: true, 4: false, 5: false, 6: false, 7: false} {
		var chID int64
		if err := pool.QueryRow(ctx, `SELECT id FROM channels WHERE number = $1`, number).Scan(&chID); err != nil {
			t.Fatal(err)
		}
		ok, err := repo.HasChannelAccess(ctx, u, chID)
		if err != nil || ok != want {
			t.Errorf("HasChannelAccess(channel %d) = %v, %v; want %v", number, ok, err, want)
		}
	}

	// Подписка с истёкшим сроком перестаёт действовать без вмешательства администратора.
	mustExec(`UPDATE subscriptions SET expires_at = now() - interval '1 second'
		WHERE user_id = $1 AND package_id = (SELECT id FROM packages WHERE name = 'p3')`, u)
	if _, got = avail(u); !slices.Equal(got, []int{1}) {
		t.Errorf("after expiry available = %v, want [1]", got)
	}
}

// Интеграционный тест: пропускается без TEST_DATABASE_URL.
func TestChannelRepository(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewChannelRepository(NewExecutor(pool))

	if _, err := pool.Exec(ctx, `INSERT INTO channels (number, name, stream_path, is_active) VALUES (2,'B','/b',true),(1,'A','/a',true),(3,'C','/c',false)`); err != nil {
		t.Fatal(err)
	}

	list, err := repo.ListActiveChannels(ctx)
	if err != nil || len(list) != 2 || list[0].Number != 1 || list[1].Number != 2 {
		t.Fatalf("ListActiveChannels = %+v, %v", list, err)
	}

	ch, err := repo.GetChannel(ctx, list[0].ID)
	if err != nil || ch.Name != "A" {
		t.Fatalf("GetChannel = %+v, %v", ch, err)
	}
	if _, err := repo.GetChannel(ctx, 1<<40); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing channel err = %v, want ErrNotFound", err)
	}

	byPath, err := repo.GetChannelByStreamPath(ctx, "/b")
	if err != nil || byPath.Name != "B" {
		t.Fatalf("GetChannelByStreamPath(/b) = %+v, %v", byPath, err)
	}
	for _, p := range []string{"/c", "/missing"} { // выключенный и несуществующий
		if _, err := repo.GetChannelByStreamPath(ctx, p); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("GetChannelByStreamPath(%q) err = %v, want ErrNotFound", p, err)
		}
	}
}

// Интеграционный тест: пропускается без TEST_DATABASE_URL.
func TestNextSubscriptionExpiry(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewChannelRepository(NewExecutor(pool))
	for _, q := range []string{
		`INSERT INTO users (login, password_hash) VALUES ('limited','h'),('forever','h'),('none','h')`,
		`INSERT INTO packages (name) VALUES ('a'),('b'),('c'),('d'),('e')`,
		`INSERT INTO subscriptions (user_id, package_id, is_active, expires_at)
			SELECT u.id, p.id, s.active, s.exp FROM users u, packages p,
			(VALUES ('limited','a', true,  now() + interval '2 hours'),
			        ('limited','b', true,  now() + interval '30 minutes'),
			        ('limited','c', true,  now() - interval '1 minute'),
			        ('limited','d', false, now() + interval '1 minute'),
			        ('limited','e', true,  NULL::timestamptz),
			        ('forever','a', true,  NULL)) AS s(login, pkg, active, exp)
			WHERE u.login = s.login AND p.name = s.pkg`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	id := func(login string) int64 {
		var uid int64
		if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE login = $1`, login).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		return uid
	}

	// Берётся ближайшая из действующих: не просроченная, не выключенная и не бессрочная.
	d, ok, err := repo.NextSubscriptionExpiry(ctx, id("limited"))
	if err != nil || !ok {
		t.Fatalf("limited: ok = %v, err = %v", ok, err)
	}
	if d < 29*time.Minute || d > 30*time.Minute {
		t.Errorf("limited: remaining = %s, want about 30m", d)
	}
	for _, login := range []string{"forever", "none"} {
		if _, ok, err := repo.NextSubscriptionExpiry(ctx, id(login)); err != nil || ok {
			t.Errorf("%s: ok = %v, err = %v; want no bound", login, ok, err)
		}
	}
}
