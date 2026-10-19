package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

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

type seedPackage struct {
	name     string
	channels []int // номера каналов
}

type seedSubscription struct {
	login, pkg string
	active     bool
	ttl        time.Duration // 0: бессрочная
}

var seedPackages = []seedPackage{
	{name: "Базовый", channels: []int{1, 2, 4, 5, 6, 77}},
	{name: "Спорт", channels: []int{3}},
}

// alice видит всё без ограничений. bob смотрит «Базовый» только короткое время после
// первого seed (демонстрация автоматического отключения), а подписка на «Спорт» выключена.
var seedSubscriptionsList = []seedSubscription{
	{login: "alice", pkg: "Базовый", active: true},
	{login: "alice", pkg: "Спорт", active: true},
	{login: "bob", pkg: "Базовый", active: true, ttl: 10 * time.Minute},
	{login: "bob", pkg: "Спорт", active: false},
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

	if err := seedSubscriptions(ctx, pool); err != nil {
		return err
	}

	log.Info("seed completed", "users", len(seedUsers), "channels", len(seedChannels),
		"packages", len(seedPackages))
	return nil
}

// seedSubscriptions добавляет пакеты, их состав и подписки. Повторный запуск ничего не меняет:
// в частности, срок подписки bob выставляется только при первом seed, а не продлевается
// при каждом старте.
func seedSubscriptions(ctx context.Context, pool *pgxpool.Pool) error {
	batch := &pgx.Batch{}
	for _, p := range seedPackages {
		batch.Queue(`INSERT INTO packages (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, p.name)
		for _, number := range p.channels {
			batch.Queue(
				`INSERT INTO package_channels (package_id, channel_id)
				 SELECT p.id, c.id FROM packages p, channels c WHERE p.name = $1 AND c.number = $2
				 ON CONFLICT DO NOTHING`, p.name, number)
		}
	}
	for _, s := range seedSubscriptionsList {
		// Срок считает БД: now() + interval, NULL остаётся бессрочной подпиской.
		batch.Queue(
			`INSERT INTO subscriptions (user_id, package_id, is_active, expires_at)
			 SELECT u.id, p.id, $3, CASE WHEN $4::bigint > 0 THEN now() + make_interval(secs => $4::bigint) END
			 FROM users u, packages p WHERE u.login = $1 AND p.name = $2
			 ON CONFLICT (user_id, package_id) DO NOTHING`,
			s.login, s.pkg, s.active, int64(s.ttl.Seconds()))
	}

	br := pool.SendBatch(ctx, batch)
	var errs []error
	for range batch.Len() {
		if _, err := br.Exec(); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, br.Close())
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("seed subscriptions: %w", err)
	}
	return nil
}
