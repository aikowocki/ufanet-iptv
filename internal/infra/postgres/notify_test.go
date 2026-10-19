package postgres

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

func TestParseEvent(t *testing.T) {
	ev, err := parseEvent("user:42")
	if err != nil || ev != (domain.InvalidationEvent{Kind: domain.InvalidateUser, ID: 42}) {
		t.Fatalf("parseEvent = %+v, %v", ev, err)
	}
	for _, bad := range []string{"", "user", "user:x", ":"} {
		if _, err := parseEvent(bad); err == nil {
			t.Errorf("parseEvent(%q): want error", bad)
		}
	}
}

// eventRecorder - обработчик событий, который складывает всё в каналы.
type eventRecorder struct {
	events chan domain.InvalidationEvent
	resets chan struct{}
}

func newEventRecorder() *eventRecorder {
	return &eventRecorder{events: make(chan domain.InvalidationEvent, 16), resets: make(chan struct{}, 16)}
}
func (r *eventRecorder) Handle(ev domain.InvalidationEvent) { r.events <- ev }
func (r *eventRecorder) Reset()                             { r.resets <- struct{}{} }

func waitReset(t *testing.T, r *eventRecorder) {
	t.Helper()
	select {
	case <-r.resets:
	case <-time.After(10 * time.Second):
		t.Fatal("listener did not (re)connect")
	}
}

func waitEvent(t *testing.T, r *eventRecorder) domain.InvalidationEvent {
	t.Helper()
	select {
	case ev := <-r.events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("event was not delivered")
		return domain.InvalidationEvent{}
	}
}

// Интеграционный тест (пропускается без TEST_DATABASE_URL): две стороны, как два экземпляра.
// Запись в транзакции публикует событие вместе с фиксацией, откат его не отправляет,
// после обрыва соединения слушатель переподключается и сбрасывает кэши.
func TestNotifyBetweenInstances(t *testing.T) {
	pool := newTestPool(t) // пропускает тест без БД
	base, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := base.Query()
	appName := "iptv_notify_test"
	q.Set("application_name", appName)
	base.RawQuery = q.Encode()

	rec := newEventRecorder()
	l := NewListener(slog.New(slog.NewTextHandler(io.Discard, nil)), base.String(), rec)
	l.MinBackoff, l.MaxBackoff = 50*time.Millisecond, 200*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() { l.Run(ctx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	waitReset(t, rec) // подключился и подписался

	exec := NewExecutor(pool)
	notifier := NewNotifier(exec)
	txm := NewTxManager(pool)

	// Фиксация: событие доходит.
	want := domain.InvalidationEvent{Kind: domain.InvalidateUser, ID: 7}
	if err := txm.Do(context.Background(), func(ctx context.Context) error { return notifier.Publish(ctx, want) }); err != nil {
		t.Fatal(err)
	}
	if got := waitEvent(t, rec); got != want {
		t.Fatalf("event = %+v, want %+v", got, want)
	}

	// Откат: события нет.
	rollback := errors.New("rollback")
	err = txm.Do(context.Background(), func(ctx context.Context) error {
		if err := notifier.Publish(ctx, domain.InvalidationEvent{Kind: domain.InvalidateChannel, ID: 1}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("Do err = %v", err)
	}
	select {
	case ev := <-rec.events:
		t.Fatalf("event %+v delivered despite rollback", ev)
	case <-time.After(500 * time.Millisecond):
	}

	// Обрыв соединения: слушатель переподключается и просит сбросить кэши целиком.
	var killed int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE application_name = $1`, appName).Scan(&killed); err != nil || killed != 1 {
		t.Fatalf("terminate listener backend: killed=%d err=%v", killed, err)
	}
	waitReset(t, rec)

	// После переподключения события снова доходят.
	want2 := domain.InvalidationEvent{Kind: domain.InvalidateSubscription, ID: 3}
	if err := notifier.Publish(context.Background(), want2); err != nil {
		t.Fatal(err)
	}
	if got := waitEvent(t, rec); got != want2 {
		t.Fatalf("event after reconnect = %+v, want %+v", got, want2)
	}
}

// Остановка по ctx закрывает соединение и завершает Run.
func TestListenerStopsOnContext(t *testing.T) {
	newTestPool(t) // пропускает тест без БД
	rec := newEventRecorder()
	l := NewListener(slog.New(slog.NewTextHandler(io.Discard, nil)), os.Getenv("TEST_DATABASE_URL"), rec)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); l.Run(ctx) }()
	waitReset(t, rec)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
}
