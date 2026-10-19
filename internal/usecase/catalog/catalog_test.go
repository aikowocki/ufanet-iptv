package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeRepo struct {
	byUser map[int64][]domain.ChannelAccess
	err    error
}

func (f fakeRepo) ListChannelsForUser(_ context.Context, userID int64) ([]domain.ChannelAccess, error) {
	return f.byUser[userID], f.err
}

func TestListPassesUsersChannels(t *testing.T) {
	repo := fakeRepo{byUser: map[int64][]domain.ChannelAccess{
		1: {{Available: true}, {Available: true}},
		2: {{Available: true}, {Available: false}},
	}}
	s := New(repo)

	for _, tt := range []struct {
		userID int64
		want   int
	}{{1, 2}, {2, 2}, {3, 0}} {
		list, err := s.List(context.Background(), domain.User{ID: tt.userID})
		if err != nil || len(list) != tt.want {
			t.Errorf("user %d: len = %d, err = %v; want %d", tt.userID, len(list), err, tt.want)
		}
	}
}

func TestListError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := New(fakeRepo{err: boom}).List(context.Background(), domain.User{ID: 1}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
}
