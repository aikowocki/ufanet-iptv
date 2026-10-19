package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeAuth struct{ err error }

func (f fakeAuth) Authenticate(_ context.Context, login, password string) (domain.User, error) {
	if f.err != nil {
		return domain.User{}, f.err
	}
	if login == "alice" && password == "secret" {
		return domain.User{ID: 1, Login: "alice", IsActive: true}, nil
	}
	return domain.User{}, domain.ErrInvalidCredentials
}

type fakeChannels struct{ err error }

func (f fakeChannels) List(context.Context, domain.User) ([]domain.Channel, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []domain.Channel{{ID: 1, Number: 1, Name: "Первый", StreamPath: "/secret/path"}}, nil
}

type fakePlayback struct{ err error }

func (f fakePlayback) Link(_ context.Context, _ domain.User, id int64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if id != 1 {
		return "", domain.ErrNotFound
	}
	return "http://s/live/1/index.m3u8", nil
}

func newAPI(log *slog.Logger, auth fakeAuth, ch fakeChannels, pb fakePlayback) http.Handler {
	return NewRouter(log, nil, Deps{Auth: auth, Channels: ch, Playback: pb})
}

func get(h http.Handler, target, user, pass string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAPI(t *testing.T) {
	boom := errors.New("pq: connection refused host=db password=hunter2")
	tests := []struct {
		name       string
		auth       fakeAuth
		ch         fakeChannels
		pb         fakePlayback
		target     string
		user, pass string
		wantStatus int
		wantCode   string
		wantHeader [2]string
	}{
		{name: "channels ok", target: "/api/v1/channels", user: "alice", pass: "secret", wantStatus: 200},
		{name: "channels no header", target: "/api/v1/channels", wantStatus: 401, wantCode: "unauthorized", wantHeader: [2]string{"Www-Authenticate", "Basic"}},
		{name: "channels wrong password", target: "/api/v1/channels", user: "alice", pass: "bad", wantStatus: 401, wantCode: "unauthorized", wantHeader: [2]string{"Www-Authenticate", "Basic"}},
		{name: "inactive user", auth: fakeAuth{err: domain.ErrInvalidCredentials}, target: "/api/v1/channels", user: "off", pass: "x", wantStatus: 401, wantCode: "unauthorized"},
		{name: "busy", auth: fakeAuth{err: domain.ErrBusy}, target: "/api/v1/channels", user: "alice", pass: "secret", wantStatus: 503, wantCode: "service_unavailable", wantHeader: [2]string{"Retry-After", "1"}},
		{name: "auth internal error", auth: fakeAuth{err: boom}, target: "/api/v1/channels", user: "alice", pass: "secret", wantStatus: 500, wantCode: "internal_error"},
		{name: "channels internal error", ch: fakeChannels{err: boom}, target: "/api/v1/channels", user: "alice", pass: "secret", wantStatus: 500, wantCode: "internal_error"},
		{name: "playback ok", target: "/api/v1/channels/1/playback", user: "alice", pass: "secret", wantStatus: 200},
		{name: "playback unauthorized", target: "/api/v1/channels/1/playback", wantStatus: 401, wantCode: "unauthorized"},
		{name: "playback bad id", target: "/api/v1/channels/abc/playback", user: "alice", pass: "secret", wantStatus: 400, wantCode: "bad_request"},
		{name: "playback zero id", target: "/api/v1/channels/0/playback", user: "alice", pass: "secret", wantStatus: 400, wantCode: "bad_request"},
		{name: "playback negative id", target: "/api/v1/channels/-5/playback", user: "alice", pass: "secret", wantStatus: 400, wantCode: "bad_request"},
		{name: "playback overflow id", target: "/api/v1/channels/99999999999999999999/playback", user: "alice", pass: "secret", wantStatus: 400, wantCode: "bad_request"},
		{name: "playback not found", target: "/api/v1/channels/2/playback", user: "alice", pass: "secret", wantStatus: 404, wantCode: "not_found"},
		{name: "playback internal error", pb: fakePlayback{err: boom}, target: "/api/v1/channels/1/playback", user: "alice", pass: "secret", wantStatus: 500, wantCode: "internal_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			h := newAPI(slog.New(slog.NewJSONHandler(&logs, nil)), tt.auth, tt.ch, tt.pb)
			rec := get(h, tt.target, tt.user, tt.pass)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q", ct)
			}
			if tt.wantHeader[0] != "" && !strings.HasPrefix(rec.Header().Get(tt.wantHeader[0]), tt.wantHeader[1]) {
				t.Errorf("header %s = %q, want prefix %q", tt.wantHeader[0], rec.Header().Get(tt.wantHeader[0]), tt.wantHeader[1])
			}
			if tt.wantCode != "" {
				var e errResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error.Code != tt.wantCode {
					t.Errorf("error body = %s (err %v), want code %q", rec.Body, err, tt.wantCode)
				}
			}
			if tt.wantStatus == 500 {
				// Клиент не видит внутренностей, а причина есть в логе.
				for _, leak := range []string{"pq:", "hunter2", "connection refused"} {
					if strings.Contains(rec.Body.String(), leak) {
						t.Errorf("500 body leaks %q: %s", leak, rec.Body)
					}
				}
				if !strings.Contains(logs.String(), "connection refused") {
					t.Errorf("cause is not logged: %s", logs.String())
				}
			}
			if strings.Contains(logs.String(), "secret") && tt.wantStatus != 500 {
				t.Errorf("log contains password: %s", logs.String())
			}
		})
	}
}

func TestChannelsJSON(t *testing.T) {
	rec := get(newAPI(slog.Default(), fakeAuth{}, fakeChannels{}, fakePlayback{}), "/api/v1/channels", "alice", "secret")
	want := `{"channels":[{"id":1,"number":1,"name":"Первый"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestPlaybackJSON(t *testing.T) {
	rec := get(newAPI(slog.Default(), fakeAuth{}, fakeChannels{}, fakePlayback{}), "/api/v1/channels/1/playback", "alice", "secret")
	want := `{"channel_id":1,"url":"http://s/live/1/index.m3u8"}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestAuthorizationNotLogged(t *testing.T) {
	var logs bytes.Buffer
	h := newAPI(slog.New(slog.NewJSONHandler(&logs, nil)), fakeAuth{}, fakeChannels{}, fakePlayback{})
	get(h, "/api/v1/channels", "alice", "secret")
	get(h, "/api/v1/channels", "alice", "wrongpass")
	for _, leak := range []string{"secret", "wrongpass", "Basic ", "YWxpY2U6"} {
		if strings.Contains(logs.String(), leak) {
			t.Errorf("log contains %q: %s", leak, logs.String())
		}
	}
}

func TestHealthzOpenWithoutAuth(t *testing.T) {
	rec := get(newAPI(slog.Default(), fakeAuth{}, fakeChannels{}, fakePlayback{}), "/healthz", "", "")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
