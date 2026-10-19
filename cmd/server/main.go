// Command server запускает HTTP API IPTV-сервиса.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/aikowocki/ufanet-iptv/internal/config"
	"github.com/aikowocki/ufanet-iptv/internal/httpserve"
	"github.com/aikowocki/ufanet-iptv/internal/infra/passhash"
	"github.com/aikowocki/ufanet-iptv/internal/infra/postgres"
	"github.com/aikowocki/ufanet-iptv/internal/infra/stream"
	"github.com/aikowocki/ufanet-iptv/internal/transport/rest"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/auth"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/catalog"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/playback"
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

	if err := postgres.Migrate(log, cfg.DB.URL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	pool, err := postgres.NewPool(ctx, log, cfg.DB.URL, cfg.DB.MaxConnections, cfg.DB.ConnectTimeout)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()

	hasher := passhash.New(cfg.HashConcurrency, cfg.HashWaitTimeout)
	// Сидер запускается только в dev: админ создаётся миграцией, а демо-данные нет.
	if cfg.Env == config.EnvDev {
		if err := postgres.Seed(ctx, log, pool, hasher); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
	}

	// Хэш для фиктивной проверки неизвестных логинов: выравнивает время ответа.
	dummyHash, err := hasher.RandomHash(ctx)
	if err != nil {
		return fmt.Errorf("dummy hash: %w", err)
	}
	signer, err := stream.New([]byte(cfg.StreamSigningKey), nil)
	if err != nil {
		return fmt.Errorf("stream signer: %w", err)
	}
	if cfg.UsesDevSigningKey() {
		log.Warn("STREAM_SIGNING_KEY is not set: using insecure dev key")
	}
	exec := postgres.NewExecutor(pool)
	users := postgres.NewUserRepository(exec)
	channels := postgres.NewChannelRepository(exec)
	srv := &http.Server{
		Handler: rest.NewRouter(log, pool.Ping, rest.Deps{
			Auth:     auth.New(users, hasher, dummyHash),
			Channels: catalog.New(channels),
			Playback: playback.New(channels, signer, cfg.StreamBaseURL, cfg.StreamTokenTTL),
		}),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("server listening", "addr", ln.Addr().String())

	if err := httpserve.Serve(ctx, srv, ln, cfg.ShutdownTimeout); err != nil {
		return err
	}
	log.Info("server stopped")
	return nil
}
