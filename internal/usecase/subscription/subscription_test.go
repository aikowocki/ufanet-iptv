package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeRepo struct {
	byUser map[int64][]domain.Subscription
	err    error
}

func (f fakeRepo) ListUserSubscriptions(_ context.Context, userID int64) ([]domain.Subscription, error) {
	return f.byUser[userID], f.err
}

func TestListIsPerUser(t *testing.T) {
	s := New(fakeRepo{byUser: map[int64][]domain.Subscription{
		1: {{PackageID: 1, Status: domain.SubscriptionActive}, {PackageID: 2, Status: domain.SubscriptionDisabled}},
	}})
	if list, err := s.List(context.Background(), domain.User{ID: 1}); err != nil || len(list) != 2 {
		t.Errorf("user 1: %d, %v; want 2", len(list), err)
	}
	if list, err := s.List(context.Background(), domain.User{ID: 2}); err != nil || len(list) != 0 {
		t.Errorf("user 2: %d, %v; want 0", len(list), err)
	}
}

func TestListError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := New(fakeRepo{err: boom}).List(context.Background(), domain.User{ID: 1}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped boom", err)
	}
}
