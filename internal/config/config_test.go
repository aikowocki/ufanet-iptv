package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 10s", cfg.ShutdownTimeout)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"HTTP_ADDR":        "127.0.0.1:9000",
		"LOG_LEVEL":        "debug",
		"SHUTDOWN_TIMEOUT": "3s",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"bad duration":      {"HTTP_READ_TIMEOUT": "soon"},
		"non-positive":      {"HTTP_IDLE_TIMEOUT": "-5s"},
		"bad level":         {"LOG_LEVEL": "verbose"},
		"several are valid": {"LOG_LEVEL": "nope", "SHUTDOWN_TIMEOUT": "0s"},
	}
	for name, vars := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(vars)); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestLoadTrimsSpaces(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"HTTP_ADDR":         "  :9090 ",
		"LOG_LEVEL":         " warn ",
		"SHUTDOWN_TIMEOUT":  " 7s ",
		"HTTP_READ_TIMEOUT": "   ", // только пробелы: значение по умолчанию
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.LogLevel != slog.LevelWarn || cfg.ShutdownTimeout != 7*time.Second {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.ReadTimeout != 10*time.Second {
		t.Errorf("ReadTimeout = %v, want default 10s", cfg.ReadTimeout)
	}
}

func TestLoadErrorMentionsVariableAndCause(t *testing.T) {
	_, err := Load(env(map[string]string{"HTTP_READ_TIMEOUT": "soon"}))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "HTTP_READ_TIMEOUT") || !strings.Contains(msg, "time: invalid duration") {
		t.Errorf("error = %q, want variable name and the original parse error", msg)
	}
}
