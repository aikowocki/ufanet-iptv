// Package config загружает конфигурацию сервиса из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
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
