package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverHidesDetails(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))

	boom := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("secret internal detail: SELECT * FROM users")
	})
	h := Chain(boom, RequestID, AccessLog(log), Recover(log))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "SELECT") || strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("response leaks internals: %s", rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error.Code != "internal_error" {
		t.Errorf("unexpected body %q (err=%v)", rec.Body.String(), err)
	}
	// Детали паники должны быть в логе, и 500 должен попасть в access-лог.
	if !strings.Contains(logs.String(), "secret internal detail") {
		t.Error("panic details are missing from log")
	}
	if !strings.Contains(logs.String(), `"status":500`) {
		t.Error("access log does not contain status 500")
	}
}

func TestRecoverRepanicsOnAbortHandler(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	abort := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler { //nolint:errorlint // sentinel panic value
			t.Fatalf("recovered %v, want http.ErrAbortHandler to propagate", rec)
		}
	}()
	Recover(log)(abort).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
