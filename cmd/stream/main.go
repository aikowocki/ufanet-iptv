// Command stream запускает учебный стрим-сервер: проверяет подпись ссылки и по запросу
// запускает ffmpeg, который крутит один и тот же ролик на всех каналах с названием канала в углу.
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
	"github.com/aikowocki/ufanet-iptv/internal/infra/postgres"
	"github.com/aikowocki/ufanet-iptv/internal/infra/stream"
	"github.com/aikowocki/ufanet-iptv/internal/streamserver"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadStream(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)

	signer, err := stream.New([]byte(cfg.StreamSigningKey), nil)
	if err != nil {
		return fmt.Errorf("stream signer: %w", err)
	}
	if cfg.UsesDevSigningKey() {
		log.Warn("STREAM_SIGNING_KEY is not set: using insecure dev key")
	}

	// Названия каналов берём из той же БД, что и API (только чтение).
	pool, err := postgres.NewPool(ctx, log, cfg.DB.URL, cfg.DB.MaxConnections, cfg.DB.ConnectTimeout)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer pool.Close()
	channels := postgres.NewChannelRepository(postgres.NewExecutor(pool))

	fontFamily := cfg.FontFamily
	if err := streamserver.LabelSupported(ctx, cfg.FFmpegBin); err != nil {
		log.Warn("channel name overlay disabled", "reason", err.Error())
		fontFamily = ""
	}
	transcoder, err := streamserver.NewTranscoder(log, channels, streamserver.Options{
		FFmpeg:       cfg.FFmpegBin,
		FontFamily:   fontFamily,
		FontsDir:     cfg.FontsDir,
		Idle:         cfg.TranscodeIdle,
		StartTimeout: cfg.TranscodeStartTimeout,
	})
	if err != nil {
		return fmt.Errorf("transcoder: %w", err)
	}
	defer transcoder.Close()

	srv := &http.Server{
		Handler:           streamserver.NewHandler(log, signer, transcoder),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	log.Info("stream server listening", "addr", ln.Addr().String())

	if err := httpserve.Serve(ctx, srv, ln, cfg.ShutdownTimeout); err != nil {
		return err
	}
	log.Info("stream server stopped")
	return nil
}
