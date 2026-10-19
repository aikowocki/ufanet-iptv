package playback

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/infra/stream"
)

type fakeRepo map[int64]domain.Channel

func (f fakeRepo) GetChannel(_ context.Context, id int64) (domain.Channel, error) {
	c, ok := f[id]
	if !ok {
		return domain.Channel{}, domain.ErrNotFound
	}
	return c, nil
}

type failSigner struct{}

func (failSigner) Sign(string, int64, time.Duration) (string, error) {
	return "", errors.New("boom")
}

func TestLink(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	signer, err := stream.New([]byte("test-key"), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	repo := fakeRepo{
		1: {ID: 1, StreamPath: "/live/1/index.m3u8", IsActive: true},
		2: {ID: 2, StreamPath: "live/2/index.m3u8", IsActive: true},
		3: {ID: 3, StreamPath: "/live/3/index.m3u8", IsActive: false},
	}
	tests := []struct {
		name     string
		base     string
		id       int64
		wantPath string
		wantErr  error
	}{
		{name: "leading slash", base: "http://s", id: 1, wantPath: "/live/1/index.m3u8"},
		{name: "no leading slash, trailing slash in base", base: "http://s/", id: 2, wantPath: "/live/2/index.m3u8"},
		{name: "base with path", base: "http://s/hls", id: 1, wantPath: "/hls/live/1/index.m3u8"},
		{name: "inactive", base: "http://s", id: 3, wantErr: domain.ErrNotFound},
		{name: "missing", base: "http://s", id: 99, wantErr: domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(repo, signer, tt.base, 5*time.Minute).Link(context.Background(), domain.User{ID: 42}, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if got != "" {
					t.Fatalf("link = %q on error", got)
				}
				return
			}
			u, err := url.Parse(got)
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != "s" || u.Path != tt.wantPath {
				t.Errorf("link = %q, want path %q", got, tt.wantPath)
			}
			if exp := u.Query().Get("expires"); exp != "1792411500" {
				t.Errorf("expires = %q, want now+5m", exp)
			}
			if uid, err := signer.Verify(got); err != nil || uid != 42 {
				t.Errorf("Verify = %d, %v; want 42, nil", uid, err)
			}
		})
	}
}

func TestLinkSignError(t *testing.T) {
	repo := fakeRepo{1: {ID: 1, StreamPath: "/a", IsActive: true}}
	_, err := New(repo, failSigner{}, "http://s", time.Minute).Link(context.Background(), domain.User{ID: 1}, 1)
	if err == nil || errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want wrapped sign error", err)
	}
}
