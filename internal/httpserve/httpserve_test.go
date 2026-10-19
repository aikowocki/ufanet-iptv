package httpserve

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// Запрос, который уже обрабатывается в момент остановки, должен завершиться успешно,
// а serve — вернуться без ошибки.
func TestServeGracefulShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			w.WriteHeader(http.StatusOK)
		}),
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveDone := make(chan error, 1)
	go func() { serveDone <- Serve(ctx, srv, ln, 5*time.Second) }()

	type result struct {
		status int
		err    error
	}
	reqDone := make(chan result, 1)
	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+ln.Addr().String(), nil)
		if err != nil {
			reqDone <- result{err: err}
			return
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			reqDone <- result{err: err}
			return
		}
		_ = resp.Body.Close()
		reqDone <- result{status: resp.StatusCode}
	}()

	<-started // запрос принят и «в работе»
	cancel()  // имитируем SIGTERM

	select {
	case err := <-serveDone:
		t.Fatalf("serve returned before in-flight request finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release) // завершаем обработку запроса

	if r := <-reqDone; r.err != nil || r.status != http.StatusOK {
		t.Fatalf("in-flight request: status=%d err=%v, want 200", r.status, r.err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after shutdown")
	}
}
