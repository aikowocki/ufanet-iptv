// Package config загружает конфигурацию сервисов из переменных окружения.
//
// Здесь лежит общее для API и стрим-сервера: режим работы, логи, ключ подписи ссылок,
// таймауты HTTP и подключение к БД. Настройки самих сервисов описаны рядом:
// server.go (cmd/server) и stream.go (cmd/stream).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Режимы работы сервисов.
const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

// MinSigningKeyLen — минимальная длина STREAM_SIGNING_KEY в prod, в байтах.
const MinSigningKeyLen = 32

// DevSigningKey используется только в dev, когда ключ не задан.
const DevSigningKey = "dev-only-insecure-stream-signing-key"

//nolint:gosec // учебный пароль для локального запуска, не секрет
const defaultDatabaseURL = "postgres://iptv:iptv@localhost:5482/iptv?sslmode=disable"

// Common — настройки, одинаковые у API и стрим-сервера.
type Common struct {
	// Env — режим работы: EnvDev (по умолчанию) или EnvProd.
	Env      string
	LogLevel slog.Level
	// StreamSigningKey — секрет HMAC для подписи ссылок на потоки. API подписывает им ссылки,
	// стрим-сервер проверяет, поэтому у обоих он должен совпадать. В prod обязателен
	// и должен быть не короче MinSigningKeyLen байт; в dev есть небезопасное значение по умолчанию.
	StreamSigningKey string
}

// UsesDevSigningKey сообщает, что в dev ключ не задан и взят небезопасный по умолчанию.
func (c Common) UsesDevSigningKey() bool {
	return c.Env == EnvDev && c.StreamSigningKey == DevSigningKey
}

// HTTPTimeouts — таймауты http.Server и остановки.
type HTTPTimeouts struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	// ShutdownTimeout ограничивает время ожидания завершения текущих запросов.
	ShutdownTimeout time.Duration
}

// Database — подключение к Postgres.
type Database struct {
	// URL — строка подключения.
	URL string
	// ConnectTimeout ограничивает ожидание доступности БД при старте.
	ConnectTimeout time.Duration
	// MaxConnections — максимальный размер пула соединений.
	MaxConnections int
}

// common читает общие настройки. Режим читается первым: от него зависит правило для ключа.
func (l *loader) common() Common {
	c := Common{
		Env:      l.oneOf("APP_ENV", EnvDev, EnvDev, EnvProd),
		LogLevel: l.level("LOG_LEVEL", slog.LevelInfo),
	}
	c.StreamSigningKey = l.signingKey("STREAM_SIGNING_KEY", c.Env)
	return c
}

func (l *loader) httpTimeouts() HTTPTimeouts {
	return HTTPTimeouts{
		ReadHeaderTimeout: l.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       l.duration("HTTP_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      l.duration("HTTP_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:       l.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:   l.duration("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}

// database читает подключение к БД. Размер пула по умолчанию у сервисов разный.
func (l *loader) database(defaultMaxConns int) Database {
	return Database{
		URL:            l.str("DATABASE_URL", defaultDatabaseURL),
		ConnectTimeout: l.duration("DB_CONNECT_TIMEOUT", 30*time.Second),
		MaxConnections: l.positiveInt("DB_MAX_CONNECTIONS", defaultMaxConns),
	}
}

// load запускает разбор через fn и объединяет все найденные ошибки в одну.
func load[T any](getenv func(string) string, fn func(l *loader) T) (T, error) {
	l := loader{getenv: getenv}
	cfg := fn(&l)
	if err := errors.Join(l.errs...); err != nil {
		var zero T
		return zero, err
	}
	return cfg, nil
}

type loader struct {
	getenv func(string) string
	errs   []error
}

// get возвращает значение переменной без пробелов по краям.
func (l *loader) get(key string) string {
	return strings.TrimSpace(l.getenv(key))
}

func (l *loader) str(key, def string) string {
	if v := l.get(key); v != "" {
		return v
	}
	return def
}

func (l *loader) duration(key string, def time.Duration) time.Duration {
	v := l.get(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid duration %q: %w", key, v, err))
		return def
	}
	if d <= 0 {
		l.errs = append(l.errs, fmt.Errorf("%s: duration must be positive, got %q", key, v))
		return def
	}
	return d
}

func (l *loader) positiveInt(key string, def int) int {
	v := l.get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		l.errs = append(l.errs, fmt.Errorf("%s: want positive integer, got %q", key, v))
		return def
	}
	return n
}

// oneOf читает переменную, значение которой должно быть одним из allowed.
// Первым аргументом после key идёт значение по умолчанию.
func (l *loader) oneOf(key, def string, allowed ...string) string {
	v := l.get(key)
	if v == "" {
		return def
	}
	if !slices.Contains(allowed, v) {
		l.errs = append(l.errs, fmt.Errorf("%s: want one of %s, got %q", key, strings.Join(allowed, "|"), v))
		return def
	}
	return v
}

func (l *loader) level(key string, def slog.Level) slog.Level {
	v := l.get(key)
	if v == "" {
		return def
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(v)); err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: want debug|info|warn|error, got %q: %w", key, v, err))
		return def
	}
	return lvl
}

// signingKey читает секрет подписи. В dev пустое значение заменяется небезопасным
// ключом по умолчанию, в prod ключ обязателен и должен быть достаточно длинным.
func (l *loader) signingKey(key, env string) string {
	v := l.get(key)
	switch {
	case v == "" && env == EnvDev:
		return DevSigningKey
	case v == "":
		l.errs = append(l.errs, fmt.Errorf("%s: required when APP_ENV=%s", key, env))
	case env != EnvDev && len(v) < MinSigningKeyLen:
		l.errs = append(l.errs, fmt.Errorf("%s: must be at least %d bytes in %s", key, MinSigningKeyLen, env))
	}
	return v
}

// httpURL читает абсолютный адрес со схемой http или https. Завершающий слэш отбрасывается.
func (l *loader) httpURL(key, def string) string {
	v := l.str(key, def)
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		l.errs = append(l.errs, fmt.Errorf("%s: want absolute http(s) URL, got %q", key, v))
		return def
	}
	return strings.TrimRight(v, "/")
}
