package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeTx struct{ calls int }

func (f *fakeTx) Do(ctx context.Context, fn func(context.Context) error) error {
	f.calls++
	return fn(ctx)
}

type fakeHasher struct{ err error }

func (f fakeHasher) Hash(_ context.Context, pw string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "hash:" + pw, nil
}

// fakeRepo реализует только то, что нужно тестам; остальное паникует через nil-встраивание.
type fakeRepo struct {
	Repository
	users        map[int64]domain.User
	packages     map[int64]domain.Package
	created      domain.User
	userPatch    UserPatch
	setChannels  []int64
	createSubErr error
}

func (f *fakeRepo) GetUser(_ context.Context, id int64) (domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (f *fakeRepo) GetPackage(_ context.Context, id int64) (domain.Package, error) {
	p, ok := f.packages[id]
	if !ok {
		return domain.Package{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakeRepo) CreateUser(_ context.Context, u domain.User) (domain.User, error) {
	f.created = u
	u.ID = 10
	return u, nil
}

func (f *fakeRepo) UpdateUser(_ context.Context, id int64, p UserPatch) (domain.User, error) {
	f.userPatch = p
	return domain.User{ID: id}, nil
}

func (f *fakeRepo) SetPackageChannels(_ context.Context, _ int64, ids []int64) error {
	f.setChannels = ids
	return nil
}

func (f *fakeRepo) CreateSubscription(_ context.Context, s domain.UserSubscription) (domain.UserSubscription, error) {
	return s, f.createSubErr
}

func newSvc(repo *fakeRepo) (*Service, *fakeTx) {
	tx := &fakeTx{}
	return New(repo, tx, fakeHasher{}), tx
}

func isValidation(err error) bool {
	var v *domain.ValidationError
	return errors.As(err, &v)
}

func TestCreateUserValidation(t *testing.T) {
	cases := []struct{ name, login, pw string }{
		{"short login", "ab", "password1"},
		{"colon in login", "a:b:c", "password1"},
		{"space in login", "a b c", "password1"},
		{"long login", strings.Repeat("a", 65), "password1"},
		{"short password", "carol", "short"},
		{"huge password", "carol", strings.Repeat("p", 129)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newSvc(&fakeRepo{})
			if _, err := s.CreateUser(context.Background(), UserInput{Login: tc.login, Password: tc.pw}); !isValidation(err) {
				t.Fatalf("err = %v, want validation error", err)
			}
		})
	}
}

func TestCreateUserHashesPassword(t *testing.T) {
	repo := &fakeRepo{}
	s, _ := newSvc(repo)
	u, err := s.CreateUser(context.Background(), UserInput{Login: "carol", Password: "password1"})
	if err != nil {
		t.Fatal(err)
	}
	if repo.created.PasswordHash != "hash:password1" || !repo.created.IsActive || repo.created.IsAdmin || u.ID != 10 {
		t.Fatalf("stored = %+v", repo.created)
	}
}

func TestCreateUserBusyHasher(t *testing.T) {
	s := New(&fakeRepo{}, &fakeTx{}, fakeHasher{err: domain.ErrBusy})
	if _, err := s.CreateUser(context.Background(), UserInput{Login: "carol", Password: "password1"}); !errors.Is(err, domain.ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestUpdateUserSelfLockout(t *testing.T) {
	off, on := false, true
	actor := domain.User{ID: 1, IsAdmin: true}
	s, _ := newSvc(&fakeRepo{})
	for name, in := range map[string]UserUpdate{
		"deactivate": {IsActive: &off},
		"demote":     {IsAdmin: &off},
	} {
		if _, err := s.UpdateUser(context.Background(), actor, 1, in); !errors.Is(err, domain.ErrSelfLockout) {
			t.Errorf("%s: err = %v, want ErrSelfLockout", name, err)
		}
	}
	// Безопасные изменения себя и любые изменения других разрешены.
	if _, err := s.UpdateUser(context.Background(), actor, 1, UserUpdate{IsActive: &on}); err != nil {
		t.Errorf("self enable: %v", err)
	}
	if _, err := s.UpdateUser(context.Background(), actor, 2, UserUpdate{IsActive: &off}); err != nil {
		t.Errorf("deactivate other: %v", err)
	}
}

func TestUpdateUserPasswordAndEmpty(t *testing.T) {
	repo := &fakeRepo{}
	s, _ := newSvc(repo)
	actor := domain.User{ID: 1}
	if _, err := s.UpdateUser(context.Background(), actor, 2, UserUpdate{}); !isValidation(err) {
		t.Errorf("empty patch err = %v, want validation", err)
	}
	short := "x"
	if _, err := s.UpdateUser(context.Background(), actor, 2, UserUpdate{Password: &short}); !isValidation(err) {
		t.Errorf("short password err = %v, want validation", err)
	}
	pw := "newpassword"
	if _, err := s.UpdateUser(context.Background(), actor, 2, UserUpdate{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	if repo.userPatch.PasswordHash == nil || *repo.userPatch.PasswordHash != "hash:newpassword" {
		t.Fatalf("patch = %+v", repo.userPatch)
	}
}

func TestChannelValidation(t *testing.T) {
	s, _ := newSvc(&fakeRepo{})
	bad := []string{"http://evil/x", "//evil/x", "../x", "a/../b", "a?x=1", "a#f", "a b", "a\\b", ""}
	for _, path := range bad {
		_, err := s.CreateChannel(context.Background(), ChannelInput{Number: 1, Name: "n", StreamPath: path})
		if !isValidation(err) {
			t.Errorf("path %q: err = %v, want validation", path, err)
		}
	}
	for _, in := range []ChannelInput{
		{Number: 0, Name: "n", StreamPath: "/ok"},
		{Number: 1, Name: "  ", StreamPath: "/ok"},
		{Number: 1, Name: "bad\nname", StreamPath: "/ok"},
	} {
		if _, err := s.CreateChannel(context.Background(), in); !isValidation(err) {
			t.Errorf("%+v: err = %v, want validation", in, err)
		}
	}
}

func TestSetPackageChannels(t *testing.T) {
	repo := &fakeRepo{packages: map[int64]domain.Package{1: {ID: 1}}}
	s, tx := newSvc(repo)
	if _, err := s.SetPackageChannels(context.Background(), 1, []int64{3, 1, 3, 2}); err != nil {
		t.Fatal(err)
	}
	if got := repo.setChannels; len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Errorf("ids = %v, want sorted unique [1 2 3]", got)
	}
	if tx.calls != 1 {
		t.Errorf("tx calls = %d, want 1", tx.calls)
	}
	if _, err := s.SetPackageChannels(context.Background(), 99, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown package err = %v, want ErrNotFound", err)
	}
	if _, err := s.SetPackageChannels(context.Background(), 1, []int64{-1}); !isValidation(err) {
		t.Errorf("negative id err = %v, want validation", err)
	}
}

func TestCreateSubscription(t *testing.T) {
	repo := &fakeRepo{
		users:    map[int64]domain.User{1: {ID: 1}},
		packages: map[int64]domain.Package{5: {ID: 5}},
	}
	s, _ := newSvc(repo)
	ctx := context.Background()

	sub, err := s.CreateSubscription(ctx, 1, SubscriptionInput{PackageID: 5})
	if err != nil || !sub.IsActive || sub.ExpiresAt != nil || sub.UserID != 1 {
		t.Fatalf("sub = %+v, err = %v", sub, err)
	}
	if _, err := s.CreateSubscription(ctx, 2, SubscriptionInput{PackageID: 5}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown user err = %v, want ErrNotFound", err)
	}
	var rErr *domain.ReferenceError
	if _, err := s.CreateSubscription(ctx, 1, SubscriptionInput{PackageID: 6}); !errors.As(err, &rErr) {
		t.Errorf("unknown package err = %v, want ReferenceError", err)
	}
	if _, err := s.CreateSubscription(ctx, 1, SubscriptionInput{}); !isValidation(err) {
		t.Errorf("missing package err = %v, want validation", err)
	}
	repo.createSubErr = domain.ErrConflict
	if _, err := s.CreateSubscription(ctx, 1, SubscriptionInput{PackageID: 5}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate err = %v, want ErrConflict", err)
	}
}
