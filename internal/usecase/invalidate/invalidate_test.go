package invalidate

import (
	"slices"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeUsers struct {
	users []int64
	all   int
}

func (f *fakeUsers) InvalidateUser(id int64) { f.users = append(f.users, id) }
func (f *fakeUsers) InvalidateAll()          { f.all++ }

type fakeCatalog struct{ n int }

func (f *fakeCatalog) InvalidateCatalog() { f.n++ }

func TestHandle(t *testing.T) {
	tests := []struct {
		name      string
		ev        domain.InvalidationEvent
		wantUsers []int64
		wantAll   int
		wantCat   int
	}{
		{"user", domain.InvalidationEvent{Kind: domain.InvalidateUser, ID: 7}, []int64{7}, 0, 0},
		{"channel", domain.InvalidationEvent{Kind: domain.InvalidateChannel, ID: 1}, nil, 0, 1},
		{"package", domain.InvalidationEvent{Kind: domain.InvalidatePackage, ID: 1}, nil, 0, 1},
		{"subscription", domain.InvalidationEvent{Kind: domain.InvalidateSubscription, ID: 1}, nil, 0, 1},
		{"unknown kind resets everything", domain.InvalidationEvent{Kind: "bogus", ID: 1}, nil, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, c := &fakeUsers{}, &fakeCatalog{}
			New(u, c).Handle(tt.ev)
			if !slices.Equal(u.users, tt.wantUsers) || u.all != tt.wantAll || c.n != tt.wantCat {
				t.Errorf("users=%v all=%d catalog=%d; want users=%v all=%d catalog=%d",
					u.users, u.all, c.n, tt.wantUsers, tt.wantAll, tt.wantCat)
			}
		})
	}
}

func TestResetClearsEverything(t *testing.T) {
	u, c := &fakeUsers{}, &fakeCatalog{}
	New(u, c).Reset()
	if u.all != 1 || c.n != 1 {
		t.Fatalf("all=%d catalog=%d, want 1 and 1", u.all, c.n)
	}
}
