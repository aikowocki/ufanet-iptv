package httpserve

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestStartPprofDisabledWhenAddrEmpty(t *testing.T) {
	// Не должно ни паниковать, ни занимать порт.
	StartPprof(t.Context(), slog.New(slog.NewTextHandler(io.Discard, nil)), "", time.Second)
}

func TestStartPprofServesAndStops(t *testing.T) {
	var lc net.ListenConfig
	probe, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	StartPprof(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), addr, time.Second)

	get := func() (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/debug/pprof/cmdline", nil)
		if err != nil {
			return 0, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}

	// Сервер стартует в горутине: даём ему немного времени.
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, err := get()
		if err == nil {
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pprof did not come up: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// После отмены контекста порт освобождается.
	cancel()
	deadline = time.Now().Add(3 * time.Second)
	for {
		ln, err := lc.Listen(t.Context(), "tcp", addr)
		if err == nil {
			_ = ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pprof port still busy after shutdown: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
