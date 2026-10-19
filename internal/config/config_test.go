package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

const testKey = "0123456789abcdef0123456789abcdef"

// env возвращает getenv поверх m. STREAM_SIGNING_KEY подставляется, если не задан
// (в prod он обязателен), кроме случая явного значения в m.
func env(m map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := m[k]; ok {
			return v
		}
		if k == "STREAM_SIGNING_KEY" {
			return testKey
		}
		return ""
	}
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

func TestLoadAppEnv(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil || cfg.Env != EnvDev {
		t.Fatalf("default Env = %q, err = %v; want dev", cfg.Env, err)
	}
	cfg, err = Load(env(map[string]string{"APP_ENV": "prod"}))
	if err != nil || cfg.Env != EnvProd {
		t.Fatalf("Env = %q, err = %v; want prod", cfg.Env, err)
	}
	if _, err = Load(env(map[string]string{"APP_ENV": "staging"})); err == nil {
		t.Error("expected error for unknown APP_ENV")
	}
}

func TestStreamBaseURL(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil || cfg.StreamBaseURL != "http://localhost:8081" {
		t.Fatalf("default = %q, %v", cfg.StreamBaseURL, err)
	}
	cfg, err = Load(env(map[string]string{"STREAM_BASE_URL": "https://cdn.example.com/hls/"}))
	if err != nil || cfg.StreamBaseURL != "https://cdn.example.com/hls" {
		t.Fatalf("override = %q, %v", cfg.StreamBaseURL, err)
	}
	for _, bad := range []string{"cdn.example.com", "ftp://x", "http://", "/relative"} {
		if _, err := Load(env(map[string]string{"STREAM_BASE_URL": bad})); err == nil {
			t.Errorf("STREAM_BASE_URL=%q: expected error", bad)
		}
	}
}

func TestStreamSigning(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil || cfg.StreamSigningKey != testKey || cfg.StreamTokenTTL != 5*time.Minute {
		t.Fatalf("defaults: key=%q ttl=%v err=%v", cfg.StreamSigningKey, cfg.StreamTokenTTL, err)
	}
	cfg, err = Load(env(map[string]string{"STREAM_TOKEN_TTL": "90s"}))
	if err != nil || cfg.StreamTokenTTL != 90*time.Second {
		t.Fatalf("ttl override = %v, %v", cfg.StreamTokenTTL, err)
	}
	if _, err = Load(env(map[string]string{"STREAM_TOKEN_TTL": "0s"})); err == nil {
		t.Error("expected error for non-positive STREAM_TOKEN_TTL")
	}

	// prod: ключ обязателен и достаточно длинный.
	for name, key := range map[string]string{"missing": "", "spaces": "   ", "short": "too-short"} {
		if _, err := Load(env(map[string]string{"APP_ENV": "prod", "STREAM_SIGNING_KEY": key})); err == nil {
			t.Errorf("prod, %s key: expected error", name)
		}
	}

	// dev (режим по умолчанию): пустой ключ заменяется значением по умолчанию, заданный сохраняется.
	cfg, err = Load(env(map[string]string{"APP_ENV": "dev", "STREAM_SIGNING_KEY": ""}))
	if err != nil || cfg.StreamSigningKey != DevSigningKey {
		t.Fatalf("dev default key = %q, %v", cfg.StreamSigningKey, err)
	}
	cfg, err = Load(env(map[string]string{"APP_ENV": "dev", "STREAM_SIGNING_KEY": "short"}))
	if err != nil || cfg.StreamSigningKey != "short" {
		t.Fatalf("dev explicit key = %q, %v", cfg.StreamSigningKey, err)
	}
}

func TestLoadStream(t *testing.T) {
	cfg, err := LoadStream(env(nil))
	if err != nil || cfg.HTTPAddr != ":8081" || cfg.Env != EnvDev || cfg.StreamSigningKey != testKey {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	if cfg.FFmpegBin != "ffmpeg" || cfg.FontFamily != "DejaVu Sans" ||
		cfg.TranscodeIdle != 30*time.Second || cfg.TranscodeStartTimeout != 15*time.Second {
		t.Fatalf("transcoder defaults: %+v", cfg)
	}
	if _, err = LoadStream(env(map[string]string{"STREAM_IDLE_TIMEOUT": "-1s"})); err == nil {
		t.Error("negative STREAM_IDLE_TIMEOUT: expected error")
	}
	cfg, err = LoadStream(env(map[string]string{"STREAM_HTTP_ADDR": ":9000", "STREAM_SIGNING_KEY": ""}))
	if err != nil || cfg.HTTPAddr != ":9000" || cfg.StreamSigningKey != DevSigningKey {
		t.Fatalf("dev without key: %+v, %v", cfg, err)
	}
	if _, err = LoadStream(env(map[string]string{"APP_ENV": "prod", "STREAM_SIGNING_KEY": ""})); err == nil {
		t.Error("prod without key: expected error")
	}
	if _, err = LoadStream(env(map[string]string{"HTTP_READ_TIMEOUT": "soon"})); err == nil {
		t.Error("bad timeout: expected error")
	}
}

// Общие настройки у API и стрим-сервера читаются одинаково, а размер пула БД по умолчанию разный.
func TestSharedSettings(t *testing.T) {
	vars := map[string]string{
		"APP_ENV": "prod", "STREAM_SIGNING_KEY": testKey, "DATABASE_URL": "postgres://db.example/x",
		"LOG_LEVEL": "warn", "HTTP_READ_TIMEOUT": "3s", "DB_CONNECT_TIMEOUT": "7s",
	}
	srv, err := Load(env(vars))
	if err != nil {
		t.Fatal(err)
	}
	str, err := LoadStream(env(vars))
	if err != nil {
		t.Fatal(err)
	}
	if srv.Common != str.Common || srv.HTTPTimeouts != str.HTTPTimeouts {
		t.Errorf("shared settings differ:\napi    %+v %+v\nstream %+v %+v", srv.Common, srv.HTTPTimeouts, str.Common, str.HTTPTimeouts)
	}
	if srv.DB.URL != "postgres://db.example/x" || str.DB.URL != srv.DB.URL || srv.DB.ConnectTimeout != 7*time.Second {
		t.Errorf("db: api %+v, stream %+v", srv.DB, str.DB)
	}
	if srv.DB.MaxConnections != 10 || str.DB.MaxConnections != 4 {
		t.Errorf("pool defaults: api %d, stream %d; want 10 and 4", srv.DB.MaxConnections, str.DB.MaxConnections)
	}
	if srv.UsesDevSigningKey() {
		t.Error("prod config reports dev key")
	}
	dev, _ := Load(env(map[string]string{"STREAM_SIGNING_KEY": ""}))
	if !dev.UsesDevSigningKey() {
		t.Error("dev config without key should report dev key")
	}
}
