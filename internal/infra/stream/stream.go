// Package stream подписывает и проверяет ссылки на HLS-потоки.
//
// Подписанная ссылка выглядит так: <url>?uid=<id>&expires=<unix>&sig=<base64url>,
// где sig = HMAC-SHA256(key, path|expires|uid), а path — путь из URL (без хоста и query).
// Контракт проверки на стороне стрим-сервера совпадает с Signer.VerifyLink.
package stream

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Имена query-параметров подписанной ссылки.
const (
	paramUID     = "uid"
	paramExpires = "expires"
	paramSig     = "sig"
)

// Ошибки проверки ссылки.
var (
	// ErrInvalid — параметров нет, они искажены или подпись не совпала.
	ErrInvalid = errors.New("invalid stream link")
	// ErrExpired — подпись верна, но срок ссылки истёк.
	ErrExpired = errors.New("stream link expired")
)

// Claims — данные, подтверждённые подписью ссылки.
type Claims struct {
	UID     int64
	Expires time.Time
}

// Signer подписывает и проверяет ссылки одним ключом.
type Signer struct {
	key []byte
	now func() time.Time
}

// New создаёт Signer. Пустой ключ не допускается. Если now == nil, используется time.Now.
func New(key []byte, now func() time.Time) (*Signer, error) {
	if len(key) == 0 {
		return nil, errors.New("stream: empty signing key")
	}
	if now == nil {
		now = time.Now
	}
	return &Signer{key: append([]byte(nil), key...), now: now}, nil
}

// Sign возвращает rawURL с параметрами uid, expires и sig. Ссылка действует ttl.
// Одноимённые параметры, уже присутствующие в rawURL, заменяются.
func (s *Signer) Sign(rawURL string, uid int64, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", fmt.Errorf("stream: ttl must be positive, got %s", ttl)
	}
	return s.SignUntil(rawURL, uid, s.now().Add(ttl))
}

// SignUntil работает как Sign, но срок действия задаётся моментом, а не длительностью.
// Стрим-сервер использует его, чтобы подписать сегменты плейлиста тем же сроком, что и сам плейлист.
func (s *Signer) SignUntil(rawURL string, uid int64, expiresAt time.Time) (string, error) {
	if uid <= 0 {
		return "", fmt.Errorf("stream: invalid uid %d", uid)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("stream: parse url: %w", err)
	}
	expires := expiresAt.Unix()

	q := u.Query()
	q.Set(paramUID, strconv.FormatInt(uid, 10))
	q.Set(paramExpires, strconv.FormatInt(expires, 10))
	q.Set(paramSig, s.sum(u.Path, expires, uid))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Verify проверяет подписанную ссылку и возвращает uid, для которого она выдана.
func (s *Signer) Verify(rawURL string) (int64, error) {
	c, err := s.VerifyLink(rawURL)
	return c.UID, err
}

// VerifyLink проверяет подписанную ссылку и возвращает uid и срок её действия.
// Подпись проверяется до срока действия, чтобы по ответу нельзя было подбирать параметры.
func (s *Signer) VerifyLink(rawURL string) (Claims, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Claims{}, ErrInvalid
	}
	q := u.Query()
	uidStr, ok1 := single(q, paramUID)
	expStr, ok2 := single(q, paramExpires)
	sig, ok3 := single(q, paramSig)
	if !ok1 || !ok2 || !ok3 {
		return Claims{}, ErrInvalid
	}
	uid, err := strconv.ParseInt(uidStr, 10, 64)
	if err != nil || uid <= 0 {
		return Claims{}, ErrInvalid
	}
	expires, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		return Claims{}, ErrInvalid
	}

	want := s.sum(u.Path, expires, uid)
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return Claims{}, ErrInvalid
	}
	exp := time.Unix(expires, 0)
	if !s.now().Before(exp) {
		return Claims{}, ErrExpired
	}
	return Claims{UID: uid, Expires: exp}, nil
}

// sum считает подпись. Path идёт первым, а expires и uid числовые, поэтому
// разделитель "|" внутри path не создаёт неоднозначности.
func (s *Signer) sum(path string, expires, uid int64) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = fmt.Fprintf(mac, "%s|%d|%d", path, expires, uid)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// single возвращает значение параметра, если он передан ровно один раз.
func single(q url.Values, key string) (string, bool) {
	v := q[key]
	if len(v) != 1 || v[0] == "" {
		return "", false
	}
	return v[0], true
}
