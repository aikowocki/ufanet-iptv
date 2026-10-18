// Command server запускает HTTP API IPTV-сервиса.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/config"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	// Отмена контекста по SIGINT/SIGTERM запускает корректную остановку.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// После первого сигнала возвращаем сигналам стандартное поведение: если остановка
	// зависла, повторный Ctrl+C завершит процесс сразу.
	context.AfterFunc(ctx, stop)

	srv := &http.Server{
		Handler:           rest.NewRouter(log),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	ln, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("server listening", "addr", ln.Addr().String())

	if err := serve(ctx, srv, ln, cfg.ShutdownTimeout); err != nil {
		return err
	}
	log.Info("server stopped")
	return nil
}

// serve запускает srv на ln и блокируется до остановки. После отмены ctx сервер
// перестаёт принимать новые соединения и ждёт завершения текущих запросов
// не дольше shutdownTimeout.
func serve(ctx context.Context, srv *http.Server, ln net.Listener, shutdownTimeout time.Duration) error {
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
