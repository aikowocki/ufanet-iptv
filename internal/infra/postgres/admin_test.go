package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/admin"
)

// Интеграционные тесты: пропускаются без TEST_DATABASE_URL.

func TestAdminUsers(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewAdminRepository(NewExecutor(pool))

	u, err := repo.CreateUser(ctx, domain.User{Login: "carol", PasswordHash: "h1", IsActive: true})
	if err != nil || u.ID == 0 || u.IsAdmin {
		t.Fatalf("CreateUser = %+v, %v", u, err)
	}
	if _, err := repo.CreateUser(ctx, domain.User{Login: "carol", PasswordHash: "h2", IsActive: true}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate login err = %v, want ErrConflict", err)
	}

	off, hash := false, "h3"
	got, err := repo.UpdateUser(ctx, u.ID, admin.UserPatch{IsActive: &off, PasswordHash: &hash})
	if err != nil || got.IsActive || got.PasswordHash != "h3" || got.IsAdmin {
		t.Fatalf("UpdateUser = %+v, %v", got, err)
	}
	// Пустой патч ничего не меняет.
	if got, err = repo.UpdateUser(ctx, u.ID, admin.UserPatch{}); err != nil || got.PasswordHash != "h3" || got.IsActive {
		t.Fatalf("empty patch = %+v, %v", got, err)
	}
	if _, err := repo.UpdateUser(ctx, 9999, admin.UserPatch{IsActive: &off}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}

	// Новый пароль снимает must_change_password у админа из миграции.
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM users WHERE login = 'admin'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateUser(ctx, id, admin.UserPatch{PasswordHash: &hash}); err != nil {
		t.Fatal(err)
	}
	var must bool
	if err := pool.QueryRow(ctx, `SELECT must_change_password FROM users WHERE id = $1`, id).Scan(&must); err != nil || must {
		t.Errorf("must_change_password = %v, err = %v; want false", must, err)
	}

	list, err := repo.ListUsers(ctx, 1, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListUsers = %v, %v", list, err)
	}
	// Сначала новые: carol создана позже админа из миграции.
	if list[0].ID != u.ID {
		t.Errorf("first user = %d, want newest %d", list[0].ID, u.ID)
	}
}

func TestAdminChannelsAndPackages(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewAdminRepository(NewExecutor(pool))

	c1, err := repo.CreateChannel(ctx, domain.Channel{Number: 1, Name: "Один", StreamPath: "/1", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := repo.CreateChannel(ctx, domain.Channel{Number: 2, Name: "Два", StreamPath: "/2", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateChannel(ctx, domain.Channel{Number: 1, Name: "Копия", StreamPath: "/x", IsActive: true}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate number err = %v, want ErrConflict", err)
	}
	// Перенос на занятый номер тоже 409.
	n := 1
	if _, err := repo.UpdateChannel(ctx, c2.ID, admin.ChannelPatch{Number: &n}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("update to taken number err = %v, want ErrConflict", err)
	}
	off, name := false, "Новое имя"
	if got, err := repo.UpdateChannel(ctx, c2.ID, admin.ChannelPatch{Name: &name, IsActive: &off}); err != nil ||
		got.Name != name || got.IsActive || got.Number != 2 {
		t.Errorf("UpdateChannel = %+v, %v", got, err)
	}
	// Админский список содержит и выключенные каналы.
	if list, err := repo.ListChannels(ctx); err != nil || len(list) != 2 {
		t.Errorf("ListChannels = %v, %v", list, err)
	}

	pkg, err := repo.CreatePackage(ctx, domain.Package{Name: "Базовый", IsActive: true})
	if err != nil || len(pkg.ChannelIDs) != 0 {
		t.Fatalf("CreatePackage = %+v, %v", pkg, err)
	}
	if _, err := repo.CreatePackage(ctx, domain.Package{Name: "Базовый"}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate package err = %v, want ErrConflict", err)
	}
	if err := repo.SetPackageChannels(ctx, pkg.ID, []int64{c1.ID, c2.ID}); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetPackage(ctx, pkg.ID); err != nil || len(got.ChannelIDs) != 2 {
		t.Fatalf("GetPackage = %+v, %v", got, err)
	}
	var rErr *domain.ReferenceError
	if err := repo.SetPackageChannels(ctx, pkg.ID, []int64{c1.ID, 9999}); !errors.As(err, &rErr) {
		t.Errorf("unknown channel err = %v, want ReferenceError", err)
	}
	if _, err := repo.GetPackage(ctx, 9999); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown package err = %v, want ErrNotFound", err)
	}
}

func TestAdminSubscriptions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewAdminRepository(NewExecutor(pool))

	u, err := repo.CreateUser(ctx, domain.User{Login: "carol", PasswordHash: "h", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := repo.CreatePackage(ctx, domain.Package{Name: "Спорт", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour).UTC().Truncate(time.Microsecond)
	s, err := repo.CreateSubscription(ctx, domain.UserSubscription{UserID: u.ID, PackageID: p.ID, IsActive: true, ExpiresAt: &exp})
	if err != nil || s.ExpiresAt == nil || !s.ExpiresAt.Equal(exp) {
		t.Fatalf("CreateSubscription = %+v, %v", s, err)
	}
	if _, err := repo.CreateSubscription(ctx, domain.UserSubscription{UserID: u.ID, PackageID: p.ID, IsActive: true}); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("duplicate err = %v, want ErrConflict", err)
	}

	// Только is_active: срок остаётся.
	off := false
	got, err := repo.UpdateSubscription(ctx, s.ID, admin.SubscriptionPatch{IsActive: &off})
	if err != nil || got.IsActive || got.ExpiresAt == nil {
		t.Fatalf("toggle = %+v, %v", got, err)
	}
	// null: бессрочная.
	got, err = repo.UpdateSubscription(ctx, s.ID, admin.SubscriptionPatch{ExpiresAt: domain.OptionalTime{Set: true}})
	if err != nil || got.ExpiresAt != nil || got.IsActive {
		t.Fatalf("clear expiry = %+v, %v", got, err)
	}
	// Новое значение.
	got, err = repo.UpdateSubscription(ctx, s.ID, admin.SubscriptionPatch{ExpiresAt: domain.OptionalTime{Set: true, Value: &exp}})
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("set expiry = %+v, %v", got, err)
	}
	if _, err := repo.UpdateSubscription(ctx, 9999, admin.SubscriptionPatch{IsActive: &off}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
	if list, err := repo.ListUserSubscriptions(ctx, u.ID); err != nil || len(list) != 1 {
		t.Errorf("ListUserSubscriptions = %v, %v", list, err)
	}
}

// Ошибка внутри Do откатывает все записи составной операции.
func TestAdminSetPackageChannelsRollsBack(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewAdminRepository(NewExecutor(pool))
	svc := admin.New(repo, NewTxManager(pool), nil)

	c, err := repo.CreateChannel(ctx, domain.Channel{Number: 1, Name: "Один", StreamPath: "/1", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := repo.CreatePackage(ctx, domain.Package{Name: "Базовый", IsActive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetPackageChannels(ctx, p.ID, []int64{c.ID}); err != nil {
		t.Fatal(err)
	}

	// Второй канал не существует: DELETE уже выполнен, INSERT падает, старый состав обязан сохраниться.
	var rErr *domain.ReferenceError
	if _, err := svc.SetPackageChannels(ctx, p.ID, []int64{9999}); !errors.As(err, &rErr) {
		t.Fatalf("err = %v, want ReferenceError", err)
	}
	got, err := repo.GetPackage(ctx, p.ID)
	if err != nil || len(got.ChannelIDs) != 1 || got.ChannelIDs[0] != c.ID {
		t.Fatalf("package after rollback = %+v, %v; want original channel kept", got, err)
	}

	if _, err := svc.SetPackageChannels(ctx, 9999, nil); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("unknown package err = %v, want ErrNotFound", err)
	}
}
