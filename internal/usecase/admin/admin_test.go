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

type recordingInvalidator struct{ ids []int64 }

func (r *recordingInvalidator) InvalidateUser(id int64) { r.ids = append(r.ids, id) }

// Смена пароля, выключение и смена роли сбрасывают кэш проверок; отказ и пустой патч - нет.
func TestUpdateUserInvalidatesCache(t *testing.T) {
	off, pw := false, "new-password-1"
	inv := &recordingInvalidator{}
	s := New(&fakeRepo{}, &fakeTx{}, fakeHasher{}, WithUserInvalidator(inv))
	actor := domain.User{ID: 1, IsAdmin: true}

	for _, in := range []UserUpdate{{IsActive: &off}, {Password: &pw}, {IsAdmin: &off}} {
		if _, err := s.UpdateUser(context.Background(), actor, 2, in); err != nil {
			t.Fatal(err)
		}
	}
	if len(inv.ids) != 3 || inv.ids[0] != 2 {
		t.Fatalf("invalidated %v, want three calls for user 2", inv.ids)
	}

	inv.ids = nil
	_, _ = s.UpdateUser(context.Background(), actor, 1, UserUpdate{IsActive: &off}) // self-lockout
	_, _ = s.UpdateUser(context.Background(), actor, 2, UserUpdate{})               // пустой патч
	if len(inv.ids) != 0 {
		t.Fatalf("invalidated %v on rejected updates", inv.ids)
	}
}

type recordingCatalog struct{ n int }

// mutationRepo добавляет к fakeRepo методы, которых ему не хватает для проверки каталога.
type mutationRepo struct{ *fakeRepo }

func (mutationRepo) CreateChannel(_ context.Context, c domain.Channel) (domain.Channel, error) {
	return c, nil
}
func (mutationRepo) UpdateChannel(_ context.Context, id int64, _ ChannelPatch) (domain.Channel, error) {
	return domain.Channel{ID: id}, nil
}
func (mutationRepo) CreatePackage(_ context.Context, p domain.Package) (domain.Package, error) {
	return p, nil
}
func (mutationRepo) UpdatePackage(_ context.Context, id int64, _ PackagePatch) (domain.Package, error) {
	return domain.Package{ID: id}, nil
}
func (mutationRepo) UpdateSubscription(_ context.Context, id int64, _ SubscriptionPatch) (domain.UserSubscription, error) {
	return domain.UserSubscription{ID: id}, nil
}

func (r *recordingCatalog) InvalidateCatalog() { r.n++ }

// Любое успешное изменение каналов, пакетов и подписок сбрасывает кэш каталога; ошибка - нет.
func TestMutationsInvalidateCatalog(t *testing.T) {
	ctx := context.Background()
	on := true
	inv := &recordingCatalog{}
	repo := mutationRepo{&fakeRepo{
		users:    map[int64]domain.User{1: {ID: 1}},
		packages: map[int64]domain.Package{1: {ID: 1}},
	}}
	s := New(repo, &fakeTx{}, fakeHasher{}, WithCatalogInvalidator(inv))

	calls := []struct {
		name string
		fn   func() error
	}{
		{"CreateChannel", func() error {
			_, err := s.CreateChannel(ctx, ChannelInput{Number: 1, Name: "Первый", StreamPath: "/live/1/index.m3u8"})
			return err
		}},
		{"UpdateChannel", func() error { _, err := s.UpdateChannel(ctx, 1, ChannelPatch{IsActive: &on}); return err }},
		{"CreatePackage", func() error { _, err := s.CreatePackage(ctx, PackageInput{Name: "Базовый"}); return err }},
		{"UpdatePackage", func() error { _, err := s.UpdatePackage(ctx, 1, PackagePatch{IsActive: &on}); return err }},
		{"SetPackageChannels", func() error { _, err := s.SetPackageChannels(ctx, 1, []int64{1}); return err }},
		{"CreateSubscription", func() error {
			_, err := s.CreateSubscription(ctx, 1, SubscriptionInput{PackageID: 1})
			return err
		}},
		{"UpdateSubscription", func() error {
			_, err := s.UpdateSubscription(ctx, 1, SubscriptionPatch{IsActive: &on})
			return err
		}},
	}
	for i, c := range calls {
		if err := c.fn(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if inv.n != i+1 {
			t.Errorf("%s: invalidations = %d, want %d", c.name, inv.n, i+1)
		}
	}

	before := inv.n
	_, _ = s.UpdateChannel(ctx, 1, ChannelPatch{}) // пустой патч - отказ
	if inv.n != before {
		t.Errorf("rejected update invalidated the cache")
	}
}

type fakePublisher struct {
	events []domain.InvalidationEvent
	err    error
}

func (f *fakePublisher) Publish(_ context.Context, ev domain.InvalidationEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, ev)
	return nil
}

// Каждая правка публикует событие своего вида внутри транзакции записи.
func TestMutationsPublishEvents(t *testing.T) {
	ctx := context.Background()
	on := true
	pub := &fakePublisher{}
	tx := &fakeTx{}
	repo := mutationRepo{&fakeRepo{
		users:    map[int64]domain.User{1: {ID: 1}},
		packages: map[int64]domain.Package{1: {ID: 1}},
	}}
	s := New(repo, tx, fakeHasher{}, WithInvalidator(pub))
	actor := domain.User{ID: 99, IsAdmin: true}

	steps := []struct {
		name string
		fn   func() error
		want domain.InvalidationEvent
	}{
		{"UpdateUser", func() error { _, err := s.UpdateUser(ctx, actor, 5, UserUpdate{IsActive: &on}); return err },
			domain.InvalidationEvent{Kind: domain.InvalidateUser, ID: 5}},
		{"UpdateChannel", func() error { _, err := s.UpdateChannel(ctx, 3, ChannelPatch{IsActive: &on}); return err },
			domain.InvalidationEvent{Kind: domain.InvalidateChannel, ID: 3}},
		{"UpdatePackage", func() error { _, err := s.UpdatePackage(ctx, 4, PackagePatch{IsActive: &on}); return err },
			domain.InvalidationEvent{Kind: domain.InvalidatePackage, ID: 4}},
		{"SetPackageChannels", func() error { _, err := s.SetPackageChannels(ctx, 1, []int64{1}); return err },
			domain.InvalidationEvent{Kind: domain.InvalidatePackage, ID: 1}},
		{"UpdateSubscription", func() error { _, err := s.UpdateSubscription(ctx, 8, SubscriptionPatch{IsActive: &on}); return err },
			domain.InvalidationEvent{Kind: domain.InvalidateSubscription, ID: 8}},
	}
	for i, st := range steps {
		before := tx.calls
		if err := st.fn(); err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}
		if len(pub.events) != i+1 || pub.events[i] != st.want {
			t.Errorf("%s: events = %+v, want last %+v", st.name, pub.events, st.want)
		}
		if tx.calls == before {
			t.Errorf("%s: write and publish must run inside a transaction", st.name)
		}
	}
}

// Если публикация не удалась, правка считается неудачной (транзакция откатывается),
// а локальные кэши не сбрасываются: данные не изменились.
func TestPublishFailureFailsMutation(t *testing.T) {
	boom := errors.New("notify failed")
	on := true
	cat := &recordingCatalog{}
	s := New(mutationRepo{&fakeRepo{}}, &fakeTx{}, fakeHasher{},
		WithInvalidator(&fakePublisher{err: boom}), WithCatalogInvalidator(cat))
	if _, err := s.UpdateChannel(context.Background(), 1, ChannelPatch{IsActive: &on}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want publish error", err)
	}
	if cat.n != 0 {
		t.Fatalf("catalog invalidated %d times after failed mutation", cat.n)
	}
}
