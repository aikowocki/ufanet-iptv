package catalog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type countingLister struct {
	calls   atomic.Int32
	release chan struct{}
	err     error
}

func (l *countingLister) List(_ context.Context, u domain.User) ([]domain.ChannelAccess, error) {
	l.calls.Add(1)
	if l.release != nil {
		<-l.release
	}
	return []domain.ChannelAccess{{Channel: domain.Channel{ID: u.ID}, Available: true}}, l.err
}

type fakeExpiry struct {
	d   time.Duration
	ok  bool
	err error
}

func (f fakeExpiry) NextSubscriptionExpiry(context.Context, int64) (time.Duration, bool, error) {
	return f.d, f.ok, f.err
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newCachedForTest(t *testing.T, l Lister, e ExpiryFinder) (*Cached, *clock) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	c, err := NewCached(l, e, 10*time.Second, clk.now)
	if err != nil {
		t.Fatal(err)
	}
	return c, clk
}

func TestCachedHitMissAndPerUser(t *testing.T) {
	l := &countingLister{}
	c, _ := newCachedForTest(t, l, fakeExpiry{})
	ctx := context.Background()

	for range 3 {
		if _, err := c.List(ctx, domain.User{ID: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if got := l.calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
	list, _ := c.List(ctx, domain.User{ID: 2})
	if l.calls.Load() != 2 || list[0].ID != 2 {
		t.Fatalf("user 2 must get own list (calls=%d, list=%+v)", l.calls.Load(), list)
	}
}

func TestCachedTTLAndSubscriptionExpiry(t *testing.T) {
	ctx := context.Background()

	l := &countingLister{}
	c, clk := newCachedForTest(t, l, fakeExpiry{})
	_, _ = c.List(ctx, domain.User{ID: 1})
	clk.add(11 * time.Second)
	_, _ = c.List(ctx, domain.User{ID: 1})
	if l.calls.Load() != 2 {
		t.Fatalf("after ttl: calls = %d, want 2", l.calls.Load())
	}

	// Подписка истекает через 3 с: запись не должна пережить этот момент.
	l = &countingLister{}
	c, clk = newCachedForTest(t, l, fakeExpiry{d: 3 * time.Second, ok: true})
	_, _ = c.List(ctx, domain.User{ID: 1})
	clk.add(2 * time.Second)
	_, _ = c.List(ctx, domain.User{ID: 1})
	if l.calls.Load() != 1 {
		t.Fatalf("before subscription expiry: calls = %d, want 1", l.calls.Load())
	}
	clk.add(2 * time.Second)
	_, _ = c.List(ctx, domain.User{ID: 1})
	if l.calls.Load() != 2 {
		t.Fatalf("after subscription expiry: calls = %d, want 2", l.calls.Load())
	}

	// Подписка уже на грани истечения: ничего не кэшируется.
	l = &countingLister{}
	c, _ = newCachedForTest(t, l, fakeExpiry{d: 0, ok: true})
	_, _ = c.List(ctx, domain.User{ID: 1})
	_, _ = c.List(ctx, domain.User{ID: 1})
	if l.calls.Load() != 2 {
		t.Fatalf("zero remaining: calls = %d, want 2 (not cached)", l.calls.Load())
	}
}

func TestCachedErrorsAreNotCached(t *testing.T) {
	boom := errors.New("boom")
	l := &countingLister{err: boom}
	c, _ := newCachedForTest(t, l, fakeExpiry{})
	for range 2 {
		if _, err := c.List(context.Background(), domain.User{ID: 1}); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want boom", err)
		}
	}
	if l.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", l.calls.Load())
	}
	c, _ = newCachedForTest(t, &countingLister{}, fakeExpiry{err: boom})
	if _, err := c.List(context.Background(), domain.User{ID: 1}); !errors.Is(err, boom) {
		t.Fatalf("expiry error: err = %v, want boom", err)
	}
}

func TestCachedCollapsesConcurrentMisses(t *testing.T) {
	l := &countingLister{release: make(chan struct{})}
	c, _ := newCachedForTest(t, l, fakeExpiry{})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := c.List(context.Background(), domain.User{ID: 1}); err != nil {
				t.Errorf("List: %v", err)
			}
		})
	}
	for l.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(l.release)
	wg.Wait()
	if got := l.calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func TestCachedInvalidateCatalog(t *testing.T) {
	l := &countingLister{}
	c, _ := newCachedForTest(t, l, fakeExpiry{})
	ctx := context.Background()
	_, _ = c.List(ctx, domain.User{ID: 1})
	_, _ = c.List(ctx, domain.User{ID: 2})
	c.InvalidateCatalog()
	_, _ = c.List(ctx, domain.User{ID: 1})
	_, _ = c.List(ctx, domain.User{ID: 2})
	if got := l.calls.Load(); got != 4 {
		t.Fatalf("calls = %d, want 4 (both users reloaded)", got)
	}
}

func TestCachedInvalidateDuringFlight(t *testing.T) {
	l := &countingLister{release: make(chan struct{})}
	c, _ := newCachedForTest(t, l, fakeExpiry{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = c.List(context.Background(), domain.User{ID: 1})
	}()
	for l.calls.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	c.InvalidateCatalog()
	close(l.release)
	<-done
	if c.cache.Len() != 0 {
		t.Fatal("result computed before invalidation was cached")
	}
}

func TestNewCachedRejectsBadTTL(t *testing.T) {
	if _, err := NewCached(&countingLister{}, fakeExpiry{}, 0, nil); err == nil {
		t.Fatal("want error for zero ttl")
	}
}
