package stream

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newSigner(t *testing.T, key string, c *clock) *Signer {
	t.Helper()
	s, err := New([]byte(key), c.now)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustSign(t *testing.T, s *Signer, raw string, uid int64, ttl time.Duration) string {
	t.Helper()
	link, err := s.Sign(raw, uid, ttl)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return link
}

func TestSignVerify(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	link := mustSign(t, s, "http://stream.example.com/live/1/index.m3u8", 7, 5*time.Minute)

	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "stream.example.com" || u.Path != "/live/1/index.m3u8" {
		t.Errorf("url changed: %s", link)
	}
	q := u.Query()
	if q.Get("uid") != "7" || q.Get("expires") != "1792411500" || q.Get("sig") == "" {
		t.Errorf("unexpected query: %s", u.RawQuery)
	}

	uid, err := s.Verify(link)
	if err != nil || uid != 7 {
		t.Fatalf("Verify = %d, %v; want 7, nil", uid, err)
	}
}

func TestVerifyExpiry(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	link := mustSign(t, s, "http://s/a.m3u8", 1, time.Minute)

	tests := []struct {
		name string
		at   time.Time
		want error
	}{
		{"before expiry", t0.Add(59 * time.Second), nil},
		{"exactly at expiry", t0.Add(time.Minute), ErrExpired},
		{"after expiry", t0.Add(time.Hour), ErrExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c.t = tt.at
			if _, err := s.Verify(link); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestVerifyTampered(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	link := mustSign(t, s, "http://s/live/1/index.m3u8", 7, time.Minute)
	u, _ := url.Parse(link)

	with := func(mutate func(*url.URL, url.Values)) string {
		cp := *u
		q := cp.Query()
		mutate(&cp, q)
		cp.RawQuery = q.Encode()
		return cp.String()
	}
	tests := map[string]string{
		"path":            with(func(u *url.URL, _ url.Values) { u.Path = "/live/2/index.m3u8" }),
		"uid":             with(func(_ *url.URL, q url.Values) { q.Set("uid", "8") }),
		"expires":         with(func(_ *url.URL, q url.Values) { q.Set("expires", "9999999999") }),
		"sig":             with(func(_ *url.URL, q url.Values) { q.Set("sig", "AAAA") }),
		"no sig":          with(func(_ *url.URL, q url.Values) { q.Del("sig") }),
		"no uid":          with(func(_ *url.URL, q url.Values) { q.Del("uid") }),
		"no expires":      with(func(_ *url.URL, q url.Values) { q.Del("expires") }),
		"duplicate uid":   link + "&uid=8",
		"bad expires":     with(func(_ *url.URL, q url.Values) { q.Set("expires", "soon") }),
		"zero uid":        with(func(_ *url.URL, q url.Values) { q.Set("uid", "0") }),
		"no query":        "http://s/live/1/index.m3u8",
		"unparseable url": "http://s/%zz",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Verify(raw); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestVerifyDifferentKey(t *testing.T) {
	c := &clock{t0}
	link := mustSign(t, newSigner(t, "key-a", c), "http://s/a.m3u8", 1, time.Minute)
	if _, err := newSigner(t, "key-b", c).Verify(link); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestVerifyIgnoresHostAndExtraParams(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	link := mustSign(t, s, "http://s/a.m3u8?token=x", 3, time.Minute)
	if !strings.Contains(link, "token=x") {
		t.Fatalf("existing query lost: %s", link)
	}
	// Стрим-сервер может стоять за другим хостом: подписан только путь.
	other := strings.Replace(link, "http://s/", "https://cdn.example.com/", 1)
	if uid, err := s.Verify(other); err != nil || uid != 3 {
		t.Fatalf("Verify = %d, %v", uid, err)
	}
}

func TestSignReplacesPreexistingParams(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	link := mustSign(t, s, "http://s/a.m3u8?uid=99&sig=evil", 4, time.Minute)
	if uid, err := s.Verify(link); err != nil || uid != 4 {
		t.Fatalf("Verify = %d, %v", uid, err)
	}
}

func TestSignInvalidArgs(t *testing.T) {
	s := newSigner(t, "k", &clock{t0})
	if _, err := s.Sign("http://s/a", 0, time.Minute); err == nil {
		t.Error("uid 0: expected error")
	}
	if _, err := s.Sign("http://s/a", 1, 0); err == nil {
		t.Error("ttl 0: expected error")
	}
	if _, err := s.Sign("http://s/%zz", 1, time.Minute); err == nil {
		t.Error("bad url: expected error")
	}
}

func TestNewEmptyKey(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestVerifyLinkReturnsClaims(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	link := mustSign(t, s, "http://s/a.m3u8", 5, time.Minute)
	got, err := s.VerifyLink(link)
	if err != nil || got.UID != 5 || !got.Expires.Equal(t0.Add(time.Minute)) {
		t.Fatalf("VerifyLink = %+v, %v", got, err)
	}
}

func TestSignUntilForOtherPath(t *testing.T) {
	c := &clock{t0}
	s := newSigner(t, "k", c)
	exp := t0.Add(90 * time.Second)
	link, err := s.SignUntil("/live/1/seg00001.ts", 5, exp)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.VerifyLink(link)
	if err != nil || got.UID != 5 || !got.Expires.Equal(exp) {
		t.Fatalf("VerifyLink = %+v, %v", got, err)
	}
	// Подпись сегмента не подходит к другому пути.
	if _, err := s.VerifyLink(strings.Replace(link, "seg00001", "seg00002", 1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
