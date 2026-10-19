package rest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/admin"
)

// adminAuth различает обычного пользователя и администратора.
type adminAuth struct{}

func (adminAuth) Authenticate(_ context.Context, login, password string) (domain.User, error) {
	switch {
	case login == "root" && password == "rootpass":
		return domain.User{ID: 1, Login: "root", IsActive: true, IsAdmin: true}, nil
	case login == "alice" && password == "secret":
		return domain.User{ID: 2, Login: "alice", IsActive: true}, nil
	}
	return domain.User{}, domain.ErrInvalidCredentials
}

// fakeAdmin возвращает заранее заданную ошибку и запоминает аргументы последнего вызова.
type fakeAdmin struct {
	err         error
	gotUser     admin.UserInput
	gotUpdate   admin.UserUpdate
	gotActor    domain.User
	gotSub      admin.SubscriptionInput
	gotSubPatch admin.SubscriptionPatch
	gotIDs      []int64
	gotLimit    int
}

func (f *fakeAdmin) ListUsers(_ context.Context, limit, _ int) ([]domain.User, error) {
	f.gotLimit = limit
	return []domain.User{{ID: 1, Login: "root", PasswordHash: "SECRET-HASH", IsActive: true, IsAdmin: true}}, f.err
}

func (f *fakeAdmin) GetUser(context.Context, int64) (domain.User, error) {
	return domain.User{ID: 3, Login: "bob", PasswordHash: "SECRET-HASH"}, f.err
}

func (f *fakeAdmin) CreateUser(_ context.Context, in admin.UserInput) (domain.User, error) {
	f.gotUser = in
	if f.err != nil {
		return domain.User{}, f.err
	}
	return domain.User{ID: 7, Login: in.Login, PasswordHash: "SECRET-HASH", IsActive: true}, nil
}

func (f *fakeAdmin) UpdateUser(_ context.Context, actor domain.User, id int64, in admin.UserUpdate) (domain.User, error) {
	f.gotActor, f.gotUpdate = actor, in
	return domain.User{ID: id, Login: "bob", PasswordHash: "SECRET-HASH"}, f.err
}

func (f *fakeAdmin) ListChannels(context.Context) ([]domain.Channel, error) {
	return []domain.Channel{{ID: 1, Number: 1, Name: "Первый", StreamPath: "/live/1/index.m3u8"}}, f.err
}

func (f *fakeAdmin) CreateChannel(_ context.Context, in admin.ChannelInput) (domain.Channel, error) {
	return domain.Channel{ID: 9, Number: in.Number, Name: in.Name, StreamPath: in.StreamPath, IsActive: true}, f.err
}

func (f *fakeAdmin) UpdateChannel(_ context.Context, id int64, _ admin.ChannelPatch) (domain.Channel, error) {
	return domain.Channel{ID: id}, f.err
}

func (f *fakeAdmin) ListPackages(context.Context) ([]domain.Package, error) {
	return []domain.Package{{ID: 1, Name: "Базовый"}}, f.err
}

func (f *fakeAdmin) CreatePackage(_ context.Context, in admin.PackageInput) (domain.Package, error) {
	return domain.Package{ID: 4, Name: in.Name, IsActive: true}, f.err
}

func (f *fakeAdmin) UpdatePackage(_ context.Context, id int64, _ admin.PackagePatch) (domain.Package, error) {
	return domain.Package{ID: id}, f.err
}

func (f *fakeAdmin) SetPackageChannels(_ context.Context, id int64, ids []int64) (domain.Package, error) {
	f.gotIDs = ids
	return domain.Package{ID: id, ChannelIDs: ids}, f.err
}

func (f *fakeAdmin) ListUserSubscriptions(context.Context, int64) ([]domain.UserSubscription, error) {
	return []domain.UserSubscription{{ID: 1, UserID: 3, PackageID: 1, IsActive: true}}, f.err
}

func (f *fakeAdmin) CreateSubscription(_ context.Context, uid int64, in admin.SubscriptionInput) (domain.UserSubscription, error) {
	f.gotSub = in
	return domain.UserSubscription{ID: 5, UserID: uid, PackageID: in.PackageID, IsActive: true, ExpiresAt: in.ExpiresAt.Value}, f.err
}

func (f *fakeAdmin) UpdateSubscription(_ context.Context, id int64, p admin.SubscriptionPatch) (domain.UserSubscription, error) {
	f.gotSubPatch = p
	return domain.UserSubscription{ID: id}, f.err
}

func newAdminAPI(f *fakeAdmin) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRouter(log, nil, Deps{
		Auth: adminAuth{}, Channels: fakeChannels{}, Playback: fakePlayback{}, Admin: f,
	})
}

func sendAs(h http.Handler, method, target, user, pass, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func asAdmin(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	return sendAs(h, method, target, "root", "rootpass", body)
}

func TestAdminRequiresAdmin(t *testing.T) {
	h := newAdminAPI(&fakeAdmin{})
	routes := []struct{ method, path string }{
		{"GET", "/api/v1/admin/users"},
		{"POST", "/api/v1/admin/users"},
		{"GET", "/api/v1/admin/users/1"},
		{"PATCH", "/api/v1/admin/users/1"},
		{"GET", "/api/v1/admin/channels"},
		{"POST", "/api/v1/admin/channels"},
		{"PATCH", "/api/v1/admin/channels/1"},
		{"GET", "/api/v1/admin/packages"},
		{"POST", "/api/v1/admin/packages"},
		{"PATCH", "/api/v1/admin/packages/1"},
		{"PUT", "/api/v1/admin/packages/1/channels"},
		{"GET", "/api/v1/admin/users/1/subscriptions"},
		{"POST", "/api/v1/admin/users/1/subscriptions"},
		{"PATCH", "/api/v1/admin/subscriptions/1"},
	}
	for _, rt := range routes {
		if rec := sendAs(h, rt.method, rt.path, "alice", "secret", `{}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s as user: status %d, want 403", rt.method, rt.path, rec.Code)
		}
		if rec := sendAs(h, rt.method, rt.path, "", "", `{}`); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s anonymous: status %d, want 401", rt.method, rt.path, rec.Code)
		}
	}
}

func TestAdminRoutesAbsentWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRouter(log, nil, Deps{Auth: adminAuth{}, Channels: fakeChannels{}, Playback: fakePlayback{}})
	if rec := asAdmin(h, "GET", "/api/v1/admin/users", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

func TestAdminCreateUserNeverReturnsPassword(t *testing.T) {
	f := &fakeAdmin{}
	rec := asAdmin(newAdminAPI(f), "POST", "/api/v1/admin/users", `{"login":"carol","password":"s3cretpass"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if f.gotUser.Login != "carol" || f.gotUser.Password != "s3cretpass" {
		t.Errorf("service got %+v", f.gotUser)
	}
	for _, leak := range []string{"s3cretpass", "SECRET-HASH", "password"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("response leaks %q: %s", leak, rec.Body)
		}
	}
	var u adminUserDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &u); err != nil || u.ID != 7 || u.Login != "carol" {
		t.Errorf("body = %s, err = %v", rec.Body, err)
	}
}

func TestAdminReadsNeverReturnHash(t *testing.T) {
	h := newAdminAPI(&fakeAdmin{})
	for _, path := range []string{"/api/v1/admin/users", "/api/v1/admin/users/3"} {
		rec := asAdmin(h, "GET", path, "")
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "SECRET-HASH") {
			t.Errorf("%s: status %d, body %s", path, rec.Code, rec.Body)
		}
	}
}

func TestAdminErrorMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&domain.ValidationError{Message: "login too short"}, http.StatusBadRequest, "bad_request"},
		{&domain.ReferenceError{Message: "package 5 does not exist"}, http.StatusUnprocessableEntity, "unprocessable_entity"},
		{domain.ErrNotFound, http.StatusNotFound, "not_found"},
		{domain.ErrConflict, http.StatusConflict, "conflict"},
		{domain.ErrSelfLockout, http.StatusConflict, "conflict"},
		{domain.ErrBusy, http.StatusServiceUnavailable, "service_unavailable"},
		{errors.New("pq: password=hunter2 refused"), http.StatusInternalServerError, "internal_error"},
	}
	for _, tc := range cases {
		rec := asAdmin(newAdminAPI(&fakeAdmin{err: tc.err}), "POST", "/api/v1/admin/channels",
			`{"number":1,"name":"x","stream_path":"/x"}`)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
			t.Errorf("%v: status %d body %s, want %d %s", tc.err, rec.Code, rec.Body, tc.status, tc.code)
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("internal error leaked: %s", rec.Body)
		}
	}
	rec := asAdmin(newAdminAPI(&fakeAdmin{err: domain.ErrBusy}), "GET", "/api/v1/admin/channels", "")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("busy response lacks Retry-After")
	}
}

func TestAdminBodyValidation(t *testing.T) {
	h := newAdminAPI(&fakeAdmin{})
	cases := []struct {
		name, body string
		status     int
	}{
		{"empty", ``, http.StatusBadRequest},
		{"malformed", `{"login":`, http.StatusBadRequest},
		{"unknown field", `{"login":"carol","password":"password1","is_root":true}`, http.StatusBadRequest},
		{"wrong type", `{"login":5,"password":"password1"}`, http.StatusBadRequest},
		{"trailing data", `{"login":"carol","password":"password1"}{}`, http.StatusBadRequest},
		{"too large", `{"login":"` + strings.Repeat("a", maxAdminBody) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		rec := asAdmin(h, "POST", "/api/v1/admin/users", tc.body)
		if rec.Code != tc.status {
			t.Errorf("%s: status %d, want %d (%s)", tc.name, rec.Code, tc.status, rec.Body)
		}
	}
}

func TestAdminInvalidIDAndQuery(t *testing.T) {
	h := newAdminAPI(&fakeAdmin{})
	for _, path := range []string{"/api/v1/admin/users/abc", "/api/v1/admin/users/0", "/api/v1/admin/users/-1"} {
		if rec := asAdmin(h, "GET", path, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", path, rec.Code)
		}
	}
	if rec := asAdmin(h, "GET", "/api/v1/admin/users?limit=x", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit: status %d, want 400", rec.Code)
	}
	f := &fakeAdmin{}
	if rec := asAdmin(newAdminAPI(f), "GET", "/api/v1/admin/users?limit=5", ""); rec.Code != http.StatusOK || f.gotLimit != 5 {
		t.Errorf("limit=5: status %d, got limit %d", rec.Code, f.gotLimit)
	}
}

func TestAdminUpdateUserPassesActor(t *testing.T) {
	f := &fakeAdmin{}
	rec := asAdmin(newAdminAPI(f), "PATCH", "/api/v1/admin/users/3", `{"is_active":false,"password":"newpassword"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if f.gotActor.ID != 1 || f.gotUpdate.IsActive == nil || *f.gotUpdate.IsActive || f.gotUpdate.IsAdmin != nil ||
		f.gotUpdate.Password == nil || *f.gotUpdate.Password != "newpassword" {
		t.Errorf("actor %+v, update %+v", f.gotActor, f.gotUpdate)
	}
	if strings.Contains(rec.Body.String(), "newpassword") {
		t.Errorf("response leaks password: %s", rec.Body)
	}
}

func TestAdminSubscriptionExpiresAt(t *testing.T) {
	f := &fakeAdmin{}
	h := newAdminAPI(f)

	rec := asAdmin(h, "POST", "/api/v1/admin/users/3/subscriptions", `{"package_id":1,"expires_at":"2027-01-02T03:04:05Z"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body)
	}
	want := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := f.gotSub.ExpiresAt; !got.Set || got.Value == nil || !got.Value.Equal(want) {
		t.Errorf("create expires_at = %+v, want %v", got, want)
	}

	// Поле не передано: срок не трогаем.
	asAdmin(h, "PATCH", "/api/v1/admin/subscriptions/5", `{"is_active":false}`)
	if f.gotSubPatch.ExpiresAt.Set || f.gotSubPatch.IsActive == nil {
		t.Errorf("absent expires_at patch = %+v", f.gotSubPatch)
	}
	// null: сделать бессрочной.
	asAdmin(h, "PATCH", "/api/v1/admin/subscriptions/5", `{"expires_at":null}`)
	if !f.gotSubPatch.ExpiresAt.Set || f.gotSubPatch.ExpiresAt.Value != nil {
		t.Errorf("null expires_at patch = %+v", f.gotSubPatch)
	}
	// Мусор вместо даты.
	if rec := asAdmin(h, "PATCH", "/api/v1/admin/subscriptions/5", `{"expires_at":"tomorrow"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad date: status %d, want 400", rec.Code)
	}
}

func TestAdminSetPackageChannels(t *testing.T) {
	f := &fakeAdmin{}
	h := newAdminAPI(f)
	rec := asAdmin(h, "PUT", "/api/v1/admin/packages/1/channels", `{"channel_ids":[1,2]}`)
	if rec.Code != http.StatusOK || len(f.gotIDs) != 2 {
		t.Fatalf("status %d, ids %v, body %s", rec.Code, f.gotIDs, rec.Body)
	}
	// Пустой список допустим (очистить пакет), отсутствие поля нет.
	if rec := asAdmin(h, "PUT", "/api/v1/admin/packages/1/channels", `{"channel_ids":[]}`); rec.Code != http.StatusOK {
		t.Errorf("empty list: status %d, want 200", rec.Code)
	}
	if rec := asAdmin(h, "PUT", "/api/v1/admin/packages/1/channels", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing field: status %d, want 400", rec.Code)
	}
}
