package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// notifyChannel — канал LISTEN/NOTIFY, по которому экземпляры сервиса обмениваются
// событиями инвалидации кэшей.
const notifyChannel = "cache_invalidate"

// Notifier публикует события инвалидации. Использует Executor, поэтому внутри транзакции
// (TxManager.Do) уведомление уходит вместе с её фиксацией, а при откате не уходит вовсе.
type Notifier struct{ db Querier }

// NewNotifier создаёт Notifier.
func NewNotifier(db Querier) *Notifier { return &Notifier{db: db} }

// Publish отправляет событие через pg_notify. Payload имеет вид "kind:id".
func (n *Notifier) Publish(ctx context.Context, ev domain.InvalidationEvent) error {
	payload := string(ev.Kind) + ":" + strconv.FormatInt(ev.ID, 10)
	if _, err := n.db.Exec(ctx, `SELECT pg_notify($1, $2)`, notifyChannel, payload); err != nil {
		return fmt.Errorf("publish %s: %w", payload, err)
	}
	return nil
}

// EventHandler получает события от Listener.
type EventHandler interface {
	// Handle вызывается на каждое событие.
	Handle(ev domain.InvalidationEvent)
	// Reset вызывается после каждого (пере)подключения: события за время обрыва
	// могли быть пропущены, поэтому кэши надо сбросить целиком.
	Reset()
}

// Listener держит выделенное соединение с LISTEN и пересоздаёт его при обрыве.
type Listener struct {
	log     *slog.Logger
	dsn     string
	handler EventHandler

	// Параметры переподключения и проверки соединения; в тестах уменьшаются.
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	PingInterval time.Duration
}

// NewListener создаёт Listener. Соединение открывается в Run.
func NewListener(log *slog.Logger, dsn string, h EventHandler) *Listener {
	return &Listener{
		log: log, dsn: dsn, handler: h,
		MinBackoff: 500 * time.Millisecond, MaxBackoff: 15 * time.Second, PingInterval: 30 * time.Second,
	}
}

// Run слушает уведомления, пока ctx не отменён; после обрыва переподключается с
// растущей паузой. Соединение закрывается при остановке.
func (l *Listener) Run(ctx context.Context) {
	backoff := l.MinBackoff
	for ctx.Err() == nil {
		listened, err := l.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if listened {
			backoff = l.MinBackoff // соединение было рабочим: начинаем с малой паузы
		}
		l.log.Warn("cache invalidation listener disconnected, will retry", "error", err, "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, l.MaxBackoff)
	}
}

// session открывает соединение, подписывается и обрабатывает события до ошибки.
// listened сообщает, что подписка успела установиться.
func (l *Listener) session(ctx context.Context) (listened bool, err error) {
	conn, err := pgx.Connect(ctx, l.dsn)
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{notifyChannel}.Sanitize()); err != nil {
		return false, fmt.Errorf("listen: %w", err)
	}
	l.log.Info("cache invalidation listener connected")
	// Сброс после LISTEN: всё, что изменится дальше, придёт событием, а что изменилось
	// раньше, уже вычищено.
	l.handler.Reset()

	for {
		waitCtx, cancel := context.WithTimeout(ctx, l.PingInterval)
		n, err := conn.WaitForNotification(waitCtx)
		cancel()
		switch {
		case err == nil:
			ev, perr := parseEvent(n.Payload)
			if perr != nil {
				l.log.Warn("cache invalidation: bad payload, resetting caches", "error", perr)
				l.handler.Reset()
				continue
			}
			l.handler.Handle(ev)
		case ctx.Err() != nil:
			return true, ctx.Err()
		case errors.Is(err, context.DeadlineExceeded):
			// Тишина: проверяем, что соединение живо, иначе оборванный TCP остался бы незамеченным.
			if perr := conn.Ping(ctx); perr != nil {
				return true, fmt.Errorf("ping: %w", perr)
			}
		default:
			return true, fmt.Errorf("wait for notification: %w", err)
		}
	}
}

func parseEvent(payload string) (domain.InvalidationEvent, error) {
	kind, id, ok := strings.Cut(payload, ":")
	if !ok {
		return domain.InvalidationEvent{}, fmt.Errorf("payload %q: want kind:id", payload)
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return domain.InvalidationEvent{}, fmt.Errorf("payload %q: bad id: %w", payload, err)
	}
	return domain.InvalidationEvent{Kind: domain.InvalidationKind(kind), ID: n}, nil
}
