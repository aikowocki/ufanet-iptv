// Package config загружает конфигурацию сервиса из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Режимы работы сервиса.
const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

// Config содержит все настройки сервиса. Значения по умолчанию подходят
// для локального запуска.
type Config struct {
	HTTPAddr string
	LogLevel slog.Level

	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	// ShutdownTimeout ограничивает время ожидания завершения текущих запросов.
	ShutdownTimeout time.Duration

	// DatabaseURL — строка подключения к Postgres.
	DatabaseURL string
	// DBConnectTimeout ограничивает ожидание доступности БД при старте.
	DBConnectTimeout time.Duration
	// DBMaxConnections — максимальный размер пула соединений.
	DBMaxConnections int
	// Env — режим работы: EnvDev или EnvProd. В dev при старте запускается сидер.
	Env string

	// HashConcurrency — сколько проверок argon2id может выполняться одновременно.
	HashConcurrency int
	// HashWaitTimeout — сколько ждать свободный слот проверки пароля.
	HashWaitTimeout time.Duration
	// StreamBaseURL — адрес стрим-сервера (http или https), к которому добавляется stream_path канала.
	StreamBaseURL string
}

// Load читает конфигурацию через getenv (обычно os.Getenv).
// Пустая переменная (в том числе из одних пробелов) означает «использовать значение
// по умолчанию»; пробелы по краям значения отбрасываются.
// Все найденные ошибки возвращаются одним значением.
func Load(getenv func(string) string) (Config, error) {
	l := loader{getenv: getenv}

	cfg := Config{
		HTTPAddr:          l.str("HTTP_ADDR", ":8080"),
		LogLevel:          l.level("LOG_LEVEL", slog.LevelInfo),
		ReadHeaderTimeout: l.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
		ReadTimeout:       l.duration("HTTP_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:      l.duration("HTTP_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:       l.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		ShutdownTimeout:   l.duration("SHUTDOWN_TIMEOUT", 10*time.Second),

		DatabaseURL:      l.str("DATABASE_URL", "postgres://iptv:iptv@localhost:5442/iptv?sslmode=disable"),
		DBConnectTimeout: l.duration("DB_CONNECT_TIMEOUT", 30*time.Second),
		DBMaxConnections: l.positiveInt("DB_MAX_CONNECTIONS", 10),
		Env:              l.oneOf("APP_ENV", EnvProd, EnvDev, EnvProd),

		HashConcurrency: l.positiveInt("HASH_CONCURRENCY", runtime.GOMAXPROCS(0)),
		HashWaitTimeout: l.duration("HASH_WAIT_TIMEOUT", 2*time.Second),
		StreamBaseURL:   l.httpURL("STREAM_BASE_URL", "http://localhost:8081"),
	}

	if err := errors.Join(l.errs...); err != nil {
		return Config{}, err
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
