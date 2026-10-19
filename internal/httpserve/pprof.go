package httpserve

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

// StartPprof запускает отдельный HTTP-сервер с профилировщиком net/http/pprof на addr.
// Пустой addr отключает профилировщик. Обработчики регистрируются на собственном mux,
// а не на http.DefaultServeMux, поэтому на основном порту pprof не появляется.
// Эндпоинты не защищены авторизацией: слушайте loopback (например 127.0.0.1:6060)
// и не публикуйте порт наружу. Сервер останавливается вместе с ctx; ошибки только логируются.
func StartPprof(ctx context.Context, log *slog.Logger, addr string, shutdownTimeout time.Duration) {
	if addr == "" {
		return
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// WriteTimeout не задан: профиль CPU и трейс пишутся десятки секунд.
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		log.Error("pprof: listen failed", "addr", addr, "err", err)
		return
	}
	log.Info("pprof listening", "addr", ln.Addr().String())

	go func() {
		if err := Serve(ctx, srv, ln, shutdownTimeout); err != nil {
			log.Error("pprof: stopped with error", "err", err)
		}
	}()
}
