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
}

// Учётные данные ТОЛЬКО для разработки и демонстрации (запускается при APP_ENV=dev).
// Администратор здесь не нужен: он создаётся миграцией create_admin.
var seedUsers = []seedUser{
	{login: "alice", password: "alice123"},
	{login: "bob", password: "bob12345"},
}

// Нумерация по тематикам: 1-19 эфирные, 20-29 кино, 30-39 спорт, 40-49 детские,
// 50-59 новости, 60-69 и 77 музыка, 80-89 познавательные. Первые каналы (1-6, 77)
// остаются прежними: на них завязаны демо-сценарии и тесты.
var seedChannels = []seedChannel{
	{1, "Первый"}, {2, "Россия 1"}, {3, "Матч ТВ"}, {4, "НТВ"},
	{5, "ТНТ"}, {6, "Пятый канал"}, {7, "Россия К"}, {8, "Россия 24"},
	{9, "Карусель"}, {10, "ОТР"}, {11, "ТВ Центр"}, {12, "РЕН ТВ"},
	{13, "СТС"}, {14, "Домашний"}, {15, "ТВ-3"}, {16, "Пятница!"},
	{17, "Звезда"}, {18, "МИР"}, {19, "ТНТ4"},

	{20, "Кино ТВ"}, {21, "Комедия"}, {22, "Мелодрама"}, {23, "Детектив"},
	{24, "Фантастика"}, {25, "Ужасы"}, {26, "Сериал"}, {27, "Русское кино"},
	{28, "Зарубежное кино"}, {29, "Классика кино"},

	{30, "Матч! Футбол 1"}, {31, "Матч! Футбол 2"}, {32, "Матч! Футбол 3"},
	{33, "Матч! Арена"}, {34, "Хоккей"}, {35, "Бокс ТВ"},
	{36, "Формула"}, {37, "Eurosport 1"},

	{40, "Мультиландия"}, {41, "Детский мир"}, {42, "Малыш"},
	{43, "Cartoon Network"}, {44, "Nickelodeon"}, {45, "Уроки"},

	{50, "РБК"}, {51, "Euronews"}, {52, "BBC World News"},
	{53, "Bloomberg"}, {54, "DW"},

	{60, "Муз-ТВ"}, {61, "Ru.TV"}, {62, "Шансон ТВ"}, {63, "Europa Plus TV"},
	{64, "Жара"}, {65, "Music Box"}, {66, "Рок ТВ"}, {77, "MTV"},

	{80, "Discovery"}, {81, "National Geographic"}, {82, "Animal Planet"},
	{83, "History"}, {84, "Моя планета"}, {85, "Viasat Explore"},
	{86, "Наука"}, {87, "Техно 24"},
}

// seedStreamPath строит путь HLS-плейлиста канала по его номеру.
func seedStreamPath(number int) string {
	return fmt.Sprintf("/live/channel%d/index.m3u8", number)
}

type seedPackage struct {
	name     string
	channels []int // номера каналов
	all      bool  // true: все каналы из seedChannels
}

type seedSubscription struct {
	login, pkg string
	active     bool
	ttl        time.Duration // 0: бессрочная
}

func numbers(ranges ...[2]int) []int {
	var out []int
	for _, r := range ranges {
		for n := r[0]; n <= r[1]; n++ {
			out = append(out, n)
		}
	}
	return out
}

// «Базовый» и «Спорт» остаются прежними по смыслу: на них построены демо-сценарии bob.
var seedPackages = []seedPackage{
	{name: "Базовый", channels: append(numbers([2]int{1, 2}, [2]int{4, 8}, [2]int{10, 18}), 77)},
	{name: "Спорт", channels: append([]int{3}, numbers([2]int{30, 37})...)},
	{name: "Кино", channels: numbers([2]int{20, 29})},
	{name: "Детский", channels: append([]int{9}, numbers([2]int{40, 45})...)},
	{name: "Новости", channels: append([]int{8}, numbers([2]int{50, 54})...)},
	{name: "Музыка", channels: append(numbers([2]int{60, 66}), 77)},
	{name: "Познавательный", channels: numbers([2]int{80, 87})},
	{name: "Максимальный", all: true},
}

// channelNumbers возвращает номера каналов пакета.
func (p seedPackage) channelNumbers() []int {
	if !p.all {
		return p.channels
	}
	out := make([]int, 0, len(seedChannels))
	for _, c := range seedChannels {
		out = append(out, c.number)
	}
	return out
}

// alice видит всё без ограничений («Максимальный» содержит все каналы). bob смотрит «Базовый» только короткое время после
// первого seed (демонстрация автоматического отключения), а подписка на «Спорт» выключена.
var seedSubscriptionsList = []seedSubscription{
	{login: "alice", pkg: "Максимальный", active: true},
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
			c.number, c.name, seedStreamPath(c.number))
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
		for _, number := range p.channelNumbers() {
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
