package streamserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/infra/stream"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

const fakePlaylist = "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:2.0,\nseg00001.ts\n#EXTINF:2.0,\nseg00002.ts\n"

// fakeSource отдаёт готовые файлы по пути или заданную ошибку.
type fakeSource struct {
	files map[string]string
	err   error
}

func (f fakeSource) Open(_ context.Context, p string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	body, ok := f.files[p]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(strings.NewReader(body)), nil
}

func defaultSource() fakeSource {
	return fakeSource{files: map[string]string{
		"/live/1/index.m3u8":  fakePlaylist,
		"/live/1/seg00001.ts": "TS1",
	}}
}

func setup(t *testing.T, src Source) (http.Handler, *stream.Signer, *time.Time) {
	t.Helper()
	now := t0
	s, err := stream.New([]byte("test-key"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(slog.New(slog.DiscardHandler), s, src), s, &now
}

// target превращает полную подписанную ссылку в то, что видит сервер: путь и query.
func target(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return u.RequestURI()
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	return rec
}

func signed(t *testing.T, s *stream.Signer, raw string, uid int64) string {
	t.Helper()
	link, err := s.Sign(raw, uid, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return target(t, link)
}

func TestPlaylistSegmentsAreSigned(t *testing.T) {
	h, s, _ := setup(t, defaultSource())
	rec := do(h, http.MethodGet, signed(t, s, "http://x/live/1/index.m3u8", 7))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}

	var segs []string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
			if strings.Contains(line, "?") {
				t.Errorf("tag line changed: %q", line)
			}
		default:
			segs = append(segs, line)
		}
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %v, want 2", segs)
	}
	for _, seg := range segs {
		u, err := url.Parse("http://x/live/1/" + seg)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := s.VerifyLink(u.String())
		if err != nil || claims.UID != 7 || !claims.Expires.Equal(t0.Add(time.Minute)) {
			t.Errorf("segment %q: claims = %+v, err = %v", seg, claims, err)
		}
	}
}

func TestSegmentWithSignedURL(t *testing.T) {
	h, s, _ := setup(t, defaultSource())
	rec := do(h, http.MethodGet, signed(t, s, "http://x/live/1/seg00001.ts", 7))
	if rec.Code != http.StatusOK || rec.Body.String() != "TS1" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp2t" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestDenied(t *testing.T) {
	h, s, now := setup(t, defaultSource())
	good := signed(t, s, "http://s/live/1/index.m3u8", 7)

	tests := []struct {
		name   string
		target string
		at     time.Time
	}{
		{"no signature", "/live/1/index.m3u8", t0},
		{"segment without signature", "/live/1/seg00001.ts", t0},
		{"other channel path", strings.Replace(good, "/live/1/", "/live/2/", 1), t0},
		{"other uid", strings.Replace(good, "uid=7", "uid=8", 1), t0},
		{"playlist signature on segment", strings.Replace(good, "index.m3u8", "seg00001.ts", 1), t0},
		{"expired", good, t0.Add(time.Minute)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*now = tt.at
			if rec := do(h, http.MethodGet, tt.target); rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestSourceErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		path string
		want int
	}{
		{"not found", ErrNotFound, "http://x/live/9/index.m3u8", http.StatusNotFound},
		{"unavailable", ErrUnavailable, "http://x/live/9/index.m3u8", http.StatusServiceUnavailable},
		{"internal error hides details", errors.New("secret detail"), "http://x/live/9/index.m3u8", http.StatusInternalServerError},
		{"unsupported extension", nil, "http://x/live/1/index.txt", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, s, _ := setup(t, fakeSource{err: tt.err})
			rec := do(h, http.MethodGet, signed(t, s, tt.path, 7))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if strings.Contains(rec.Body.String(), "secret") {
				t.Errorf("body leaks details: %q", rec.Body.String())
			}
			if tt.want == http.StatusServiceUnavailable && rec.Header().Get("Retry-After") == "" {
				t.Error("503 without Retry-After")
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h, s, _ := setup(t, defaultSource())
	if rec := do(h, http.MethodPost, signed(t, s, "http://x/live/1/index.m3u8", 7)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}
