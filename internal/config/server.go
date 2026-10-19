package config

import (
	"runtime"
	"time"
)

// Config содержит настройки API (cmd/server). Значения по умолчанию подходят
// для локального запуска.
type Config struct {
	Common
	HTTPTimeouts
	DB Database

	HTTPAddr string

	// HashConcurrency — сколько проверок argon2id может выполняться одновременно.
	HashConcurrency int
	// HashWaitTimeout — сколько ждать свободный слот проверки пароля.
	HashWaitTimeout time.Duration
	// StreamBaseURL — адрес стрим-сервера (http или https), к которому добавляется stream_path канала.
	StreamBaseURL string
	// StreamTokenTTL — срок действия подписанной ссылки.
	StreamTokenTTL time.Duration
}

// Load читает конфигурацию API через getenv (обычно os.Getenv).
// Пустая переменная (в том числе из одних пробелов) означает «использовать значение
// по умолчанию»; пробелы по краям значения отбрасываются.
// Все найденные ошибки возвращаются одним значением.
func Load(getenv func(string) string) (Config, error) {
	return load(getenv, func(l *loader) Config {
		return Config{
			Common:       l.common(),
			HTTPTimeouts: l.httpTimeouts(),
			DB:           l.database(10),

			HTTPAddr: l.str("HTTP_ADDR", ":8080"),

			HashConcurrency: l.positiveInt("HASH_CONCURRENCY", runtime.GOMAXPROCS(0)),
			HashWaitTimeout: l.duration("HASH_WAIT_TIMEOUT", 2*time.Second),
			StreamBaseURL:   l.httpURL("STREAM_BASE_URL", "http://localhost:8081"),
			StreamTokenTTL:  l.duration("STREAM_TOKEN_TTL", 5*time.Minute),
		}
	})
}
