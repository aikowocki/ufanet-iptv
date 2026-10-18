// Package postgres содержит подключение к Postgres, миграции и начальные данные.
package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/golang-migrate/migrate/v4"
	// Драйвер БД для golang-migrate (регистрируется схемой pgx5://).
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	retryInterval = time.Second
	pingTimeout   = 2 * time.Second
)

// NewPool создаёт пул соединений и ждёт доступности БД не дольше connectTimeout.
// Повторные попытки нужны, потому что при совместном старте (docker compose)
// Postgres может ещё принимать подключения не сразу.
func NewPool(ctx context.Context, log *slog.Logger, databaseURL string, maxConns int, connectTimeout time.Duration) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// Ошибка разбора может содержать часть строки подключения с паролем.
		return nil, errors.New("parse database url: invalid format")
	}
	cfg.MaxConns = int32(min(maxConns, 1<<30)) //nolint:gosec // значение ограничено сверху

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	waitCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	for {
		pingCtx, pingCancel := context.WithTimeout(waitCtx, pingTimeout)
		err := pool.Ping(pingCtx)
		pingCancel()
		if err == nil {
			return pool, nil
		}
		log.Warn("database is not ready", "error", err)

		select {
		case <-waitCtx.Done():
			pool.Close()
			return nil, fmt.Errorf("database is unavailable: %w", errors.Join(err, waitCtx.Err()))
		case <-time.After(retryInterval):
		}
	}
}

// Migrate применяет встроенные миграции. golang-migrate берёт advisory lock,
// поэтому одновременный старт нескольких экземпляров безопасен.
func Migrate(log *slog.Logger, databaseURL string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("open migrations: %w", err)
	}

	dsn, err := migrateURL(databaseURL)
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
	if err != nil {
		return fmt.Errorf("init migrate: %w", err)
	}
	defer func() {
		if srcErr, dbErr := m.Close(); srcErr != nil || dbErr != nil {
			log.Warn("close migrate", "source_error", srcErr, "database_error", dbErr)
		}
	}()

	switch err := m.Up(); {
	case errors.Is(err, migrate.ErrNoChange):
		log.Info("migrations are up to date")
	case err != nil:
		return fmt.Errorf("apply migrations: %w", err)
	default:
		v, dirty, _ := m.Version()
		log.Info("migrations applied", "version", v, "dirty", dirty)
	}
	return nil
}

// migrateURL заменяет схему postgres:// на pgx5://, которую ожидает драйвер.
func migrateURL(databaseURL string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", errors.New("parse database url: invalid format")
	}
	switch u.Scheme {
	case "postgres", "postgresql", "pgx5":
		u.Scheme = "pgx5"
	default:
		return "", fmt.Errorf("unsupported database url scheme %q", u.Scheme)
	}
	return u.String(), nil
}
