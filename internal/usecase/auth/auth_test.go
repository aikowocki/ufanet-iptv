package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

type fakeRepo struct {
	users map[string]domain.User
	err   error
}

func (f fakeRepo) GetUserByLogin(_ context.Context, login string) (domain.User, error) {
	if f.err != nil {
		return domain.User{}, f.err
	}
	u, ok := f.users[login]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

// fakeVerifier считает хэш "h:<пароль>" верным и запоминает, какие хэши проверялись.
type fakeVerifier struct {
	err    error
	hashes []string
}

func (f *fakeVerifier) Verify(_ context.Context, password, hash string) (bool, error) {
	f.hashes = append(f.hashes, hash)
	if f.err != nil {
		return false, f.err
	}
	return hash == "h:"+password, nil
}

func TestAuthenticate(t *testing.T) {
	repo := fakeRepo{users: map[string]domain.User{
		"alice": {ID: 1, Login: "alice", PasswordHash: "h:secret", IsActive: true},
		"off":   {ID: 2, Login: "off", PasswordHash: "h:secret", IsActive: false},
	}}
	errBoom := errors.New("boom")
	tests := []struct {
		name, login, password string
		repo                  fakeRepo
		verr                  error
		wantErr               error
		wantID                int64
		wantHash              string
	}{
		{name: "ok", login: "alice", password: "secret", repo: repo, wantID: 1, wantHash: "h:secret"},
		{name: "wrong password", login: "alice", password: "bad", repo: repo, wantErr: domain.ErrInvalidCredentials, wantHash: "h:secret"},
		{name: "unknown user still verifies dummy", login: "nobody", password: "secret", repo: repo, wantErr: domain.ErrInvalidCredentials, wantHash: "dummy"},
		{name: "inactive user", login: "off", password: "secret", repo: repo, wantErr: domain.ErrInvalidCredentials, wantHash: "h:secret"},
		{name: "repo error", login: "alice", password: "secret", repo: fakeRepo{err: errBoom}, wantErr: errBoom},
		{name: "busy", login: "alice", password: "secret", repo: repo, verr: domain.ErrBusy, wantErr: domain.ErrBusy},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &fakeVerifier{err: tt.verr}
			got, err := New(tt.repo, v, "dummy").Authenticate(context.Background(), tt.login, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.ID != tt.wantID {
				t.Errorf("user id = %d, want %d", got.ID, tt.wantID)
			}
			if tt.wantHash != "" && (len(v.hashes) != 1 || v.hashes[0] != tt.wantHash) {
				t.Errorf("verified hashes = %v, want [%s]", v.hashes, tt.wantHash)
			}
		})
	}
}
