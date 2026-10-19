package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PasswordHasher хэширует пароли тестовых пользователей.
type PasswordHasher interface {
	Hash(ctx context.Context, password string) (string, error)
}

type seedUser struct {
	login    string
	password string
	isAdmin  bool
}

type seedChannel struct {
	number int
	name   string
	path   string
}

// Учётные данные ТОЛЬКО для разработки и демонстрации (запускается при APP_ENV=dev).
// Администратор здесь не нужен: он создаётся миграцией create_admin.
var seedUsers = []seedUser{
	{login: "alice", password: "alice123"},
	{login: "bob", password: "bob12345"},
}

var seedChannels = []seedChannel{
	{1, "Первый", "/live/channel1/index.m3u8"},
	{2, "Россия 1", "/live/channel2/index.m3u8"},
	{3, "Матч ТВ", "/live/channel3/index.m3u8"},
	{4, "НТВ", "/live/channel4/index.m3u8"},
	{5, "ТНТ", "/live/channel5/index.m3u8"},
	{6, "Пятый канал", "/live/channel6/index.m3u8"},
	{77, "MTV", "/live/channel77/index.m3u8"},
}

// Seed добавляет демо-данные для разработки: абонентов и каналы. Идемпотентен: существующие записи
// (по login и number) не изменяются, повторный запуск ничего не дублирует.
// Пароль хэшируется только для отсутствующих пользователей, чтобы повторные
// старты не тратили CPU и память на argon2id.
func Seed(ctx context.Context, log *slog.Logger, pool *pgxpool.Pool, hasher PasswordHasher) error {
	for _, u := range seedUsers {
		var exists bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE login = $1)`, u.login).Scan(&exists)
		if err != nil {
			return fmt.Errorf("seed user %q: check: %w", u.login, err)
		}
		if exists {
			continue
		}

		hash, err := hasher.Hash(ctx, u.password)
		if err != nil {
			return fmt.Errorf("seed user %q: hash: %w", u.login, err)
		}
		// ON CONFLICT закрывает гонку, если другой экземпляр вставил запись между проверкой и вставкой.
		if _, err := pool.Exec(ctx,
			`INSERT INTO users (login, password_hash, is_admin) VALUES ($1, $2, $3)
			 ON CONFLICT (login) DO NOTHING`,
			u.login, hash, u.isAdmin); err != nil {
			return fmt.Errorf("seed user %q: insert: %w", u.login, err)
		}
	}

	batch := &pgx.Batch{}
	for _, c := range seedChannels {
		batch.Queue(
			`INSERT INTO channels (number, name, stream_path) VALUES ($1, $2, $3)
			 ON CONFLICT (number) DO NOTHING`,
			c.number, c.name, c.path)
	}
	br := pool.SendBatch(ctx, batch)
	var errs []error
	for range seedChannels {
		if _, err := br.Exec(); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, br.Close())
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("seed channels: %w", err)
	}

	log.Info("seed completed", "users", len(seedUsers), "channels", len(seedChannels))
	return nil
}
