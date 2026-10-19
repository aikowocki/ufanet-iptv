package auth

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// countingAuth считает вызовы и принимает только пароль "secret".
type countingAuth struct {
	calls   atomic.Int32
	user    domain.User
	release chan struct{} // если задан, вызов ждёт закрытия
}

func (a *countingAuth) Authenticate(_ context.Context, _, password string) (domain.User, error) {
	a.calls.Add(1)
	if a.release != nil {
		<-a.release
	}
	if password != "secret" {
		return domain.User{}, domain.ErrInvalidCredentials
	}
	return a.user, nil
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newCached(t *testing.T, next Authenticator) (*Cached, *fakeClock) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	c, err := NewCached(next, 45*time.Second, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	return c, clk
}

func TestCachedHitAndMiss(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1, Login: "alice", IsActive: true}}
	c, _ := newCached(t, next)
	ctx := context.Background()

	for range 3 {
		u, err := c.Authenticate(ctx, "alice", "secret")
		if err != nil || u.ID != 1 {
			t.Fatalf("Authenticate = %+v, %v", u, err)
		}
	}
	if got := next.calls.Load(); got != 1 {
		t.Fatalf("next called %d times, want 1 (hits must not reach Verify)", got)
	}
	// Другой логин с тем же паролем - другой ключ.
	if _, err := c.Authenticate(ctx, "bob", "secret"); err != nil {
		t.Fatal(err)
	}
	if got := next.calls.Load(); got != 2 {
		t.Fatalf("next called %d times, want 2", got)
	}
}

func TestCachedExpires(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}}
	c, clk := newCached(t, next)
	ctx := context.Background()

	_, _ = c.Authenticate(ctx, "alice", "secret")
	clk.add(44 * time.Second)
	_, _ = c.Authenticate(ctx, "alice", "secret")
	if got := next.calls.Load(); got != 1 {
		t.Fatalf("before TTL: next called %d times, want 1", got)
	}
	clk.add(2 * time.Second)
	_, _ = c.Authenticate(ctx, "alice", "secret")
	if got := next.calls.Load(); got != 2 {
		t.Fatalf("after TTL: next called %d times, want 2", got)
	}
}

func TestCachedDoesNotCacheFailures(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}}
	c, _ := newCached(t, next)
	ctx := context.Background()

	for range 3 {
		if _, err := c.Authenticate(ctx, "alice", "wrong"); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Fatalf("err = %v, want ErrInvalidCredentials", err)
		}
	}
	if got := next.calls.Load(); got != 3 {
		t.Fatalf("next called %d times, want 3 (failures must not be cached)", got)
	}
	if c.cache.Len() != 0 {
		t.Fatalf("cache has %d entries after failures", c.cache.Len())
	}
	// Неверный пароль не должен проходить по закэшированному верному.
	_, _ = c.Authenticate(ctx, "alice", "secret")
	if _, err := c.Authenticate(ctx, "alice", "wrong"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Fatalf("wrong password after success: err = %v", err)
	}
}

func TestCachedCollapsesConcurrentMisses(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}, release: make(chan struct{})}
	c, _ := newCached(t, next)

	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, err := c.Authenticate(context.Background(), "alice", "secret"); err != nil {
				t.Errorf("Authenticate: %v", err)
			}
		})
	}
	// Дать всем горутинам встать в очередь за одним вызовом.
	deadline := time.Now().Add(2 * time.Second)
	for next.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(next.release)
	wg.Wait()

	if got := next.calls.Load(); got != 1 {
		t.Fatalf("next called %d times for %d concurrent misses, want 1", got, n)
	}
}

func TestCachedWaiterHonorsOwnContext(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}, release: make(chan struct{})}
	c, _ := newCached(t, next)
	defer close(next.release)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Authenticate(ctx, "alice", "secret"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCachedInvalidateUser(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}}
	c, _ := newCached(t, next)
	ctx := context.Background()

	_, _ = c.Authenticate(ctx, "alice", "secret")
	c.InvalidateUser(2) // чужой пользователь не затрагивается
	_, _ = c.Authenticate(ctx, "alice", "secret")
	if got := next.calls.Load(); got != 1 {
		t.Fatalf("after invalidating another user: calls = %d, want 1", got)
	}
	c.InvalidateUser(1)
	_, _ = c.Authenticate(ctx, "alice", "secret")
	if got := next.calls.Load(); got != 2 {
		t.Fatalf("after invalidation: calls = %d, want 2", got)
	}
}

// Результат проверки, начатой до инвалидации, не должен попасть в кэш.
func TestCachedInvalidateDuringFlight(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}, release: make(chan struct{})}
	c, _ := newCached(t, next)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.Authenticate(context.Background(), "alice", "secret")
	}()
	for next.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	c.InvalidateUser(1)
	close(next.release)
	<-done

	if c.cache.Len() != 0 {
		t.Fatal("result computed before invalidation was cached")
	}
}

func TestNewCachedRejectsBadTTL(t *testing.T) {
	if _, err := NewCached(&countingAuth{}, 0, nil); err == nil {
		t.Fatal("want error for zero ttl")
	}
}

func TestCachedInvalidateAll(t *testing.T) {
	next := &countingAuth{user: domain.User{ID: 1}}
	c, _ := newCached(t, next)
	ctx := context.Background()
	_, _ = c.Authenticate(ctx, "alice", "secret")
	_, _ = c.Authenticate(ctx, "bob", "secret")
	c.InvalidateAll()
	if c.cache.Len() != 0 {
		t.Fatalf("cache has %d entries after InvalidateAll", c.cache.Len())
	}
	_, _ = c.Authenticate(ctx, "alice", "secret")
	if got := next.calls.Load(); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}
