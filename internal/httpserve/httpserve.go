// Package httpserve запускает http.Server с корректной остановкой. Общий код для cmd/server и cmd/stream.
package httpserve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Serve запускает srv на ln и блокируется до остановки. После отмены ctx сервер
// перестаёт принимать новые соединения и ждёт завершения текущих запросов
// не дольше shutdownTimeout.
func Serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		// Сервер упал сам, не из-за остановки.
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	// Контекст ctx уже отменён, поэтому для остановки нужен новый.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close() // не дождались: закрываем оставшиеся соединения принудительно
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
