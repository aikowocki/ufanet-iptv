package playback

import (
	"context"
	"errors"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeRepo map[int64]domain.Channel

func (f fakeRepo) GetChannel(_ context.Context, id int64) (domain.Channel, error) {
	c, ok := f[id]
	if !ok {
		return domain.Channel{}, domain.ErrNotFound
	}
	return c, nil
}

func TestLink(t *testing.T) {
	repo := fakeRepo{
		1: {ID: 1, StreamPath: "/live/1/index.m3u8", IsActive: true},
		2: {ID: 2, StreamPath: "live/2/index.m3u8", IsActive: true},
		3: {ID: 3, StreamPath: "/live/3/index.m3u8", IsActive: false},
	}
	tests := []struct {
		name    string
		base    string
		id      int64
		want    string
		wantErr error
	}{
		{name: "leading slash", base: "http://s", id: 1, want: "http://s/live/1/index.m3u8"},
		{name: "no leading slash, trailing slash in base", base: "http://s/", id: 2, want: "http://s/live/2/index.m3u8"},
		{name: "inactive", base: "http://s", id: 3, wantErr: domain.ErrNotFound},
		{name: "missing", base: "http://s", id: 99, wantErr: domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(repo, tt.base).Link(context.Background(), domain.User{}, tt.id)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("Link = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
