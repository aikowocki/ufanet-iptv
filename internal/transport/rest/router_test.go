package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/transport/rest/response"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

type errResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
	return rec
}

func TestHealthz(t *testing.T) {
	rec := do(t, NewRouter(discardLogger(), nil, Deps{}), http.MethodGet, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header is missing")
	}
}

func TestRouterErrorsAreJSON(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{"unknown path", http.MethodGet, "/nope", http.StatusNotFound, response.CodeNotFound, ""},
		{"wrong method", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, response.CodeMethodNotAllowed, "GET"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, NewRouter(discardLogger(), nil, Deps{}), tt.method, tt.target)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
			if tt.wantAllow != "" && !strings.Contains(rec.Header().Get("Allow"), tt.wantAllow) {
				t.Errorf("Allow = %q, want it to contain %q", rec.Header().Get("Allow"), tt.wantAllow)
			}
			var body errResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
			}
			if body.Error.Code != tt.wantCode || body.Error.Message == "" {
				t.Errorf("error body = %+v, want code %q and non-empty message", body.Error, tt.wantCode)
			}
		})
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name       string
		check      ReadyCheck
		wantStatus int
	}{
		{"no checks", nil, http.StatusOK},
		{"healthy", func(context.Context) error { return nil }, http.StatusOK},
		{"db down", func(context.Context) error { return errors.New("conn refused: secret-host") }, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, NewRouter(discardLogger(), tt.check, Deps{}), http.MethodGet, "/readyz")
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if strings.Contains(rec.Body.String(), "secret-host") {
				t.Errorf("response leaks internal error: %q", rec.Body.String())
			}
		})
	}
}
