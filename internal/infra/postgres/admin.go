package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
	"github.com/aikowocki/ufanet-iptv/internal/usecase/admin"
)

const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
)

// AdminRepository изменяет пользователей, каналы, пакеты и подписки. Все запросы параметризованы.
// Частичные обновления делаются одним UPDATE: COALESCE оставляет поле, если параметр NULL.
type AdminRepository struct{ db Querier }

// NewAdminRepository создаёт AdminRepository.
func NewAdminRepository(db Querier) *AdminRepository { return &AdminRepository{db: db} }

// mapWriteErr переводит ошибки Postgres в доменные.
func mapWriteErr(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch pgErr.Code {
		case codeUniqueViolation:
			return domain.ErrConflict
		case codeForeignKeyViolation:
			return &domain.ReferenceError{Message: "referenced record does not exist"}
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}

const userCols = `id, login, password_hash, is_active, is_admin`

func scanUser(row pgx.Row) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Login, &u.PasswordHash, &u.IsActive, &u.IsAdmin)
	return u, err
}

// ListUsers возвращает страницу пользователей от новых к старым (по created_at, затем по id).
func (r *AdminRepository) ListUsers(ctx context.Context, limit, offset int) ([]domain.User, error) {
	rows, err := r.db.Query(ctx, `SELECT `+userCols+` FROM users ORDER BY created_at DESC, id DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()
	var list []domain.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		list = append(list, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return list, nil
}

// GetUser возвращает пользователя по id или domain.ErrNotFound.
func (r *AdminRepository) GetUser(ctx context.Context, id int64) (domain.User, error) {
	u, err := scanUser(r.db.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
	if err != nil {
		return domain.User{}, mapWriteErr("query user", err)
	}
	return u, nil
}

// CreateUser добавляет пользователя. Занятый логин даёт domain.ErrConflict.
func (r *AdminRepository) CreateUser(ctx context.Context, u domain.User) (domain.User, error) {
	out, err := scanUser(r.db.QueryRow(ctx,
		`INSERT INTO users (login, password_hash, is_active, is_admin) VALUES ($1, $2, $3, $4)
		 RETURNING `+userCols, u.Login, u.PasswordHash, u.IsActive, u.IsAdmin))
	if err != nil {
		return domain.User{}, mapWriteErr("insert user", err)
	}
	return out, nil
}

// UpdateUser меняет заданные поля. Новый пароль сбрасывает must_change_password:
// его назначил администратор, а не известный начальный.
func (r *AdminRepository) UpdateUser(ctx context.Context, id int64, p admin.UserPatch) (domain.User, error) {
	u, err := scanUser(r.db.QueryRow(ctx,
		`UPDATE users SET
		     is_active     = COALESCE($2::boolean, is_active),
		     is_admin      = COALESCE($3::boolean, is_admin),
		     password_hash = COALESCE($4::text, password_hash),
		     must_change_password = CASE WHEN $4::text IS NULL THEN must_change_password ELSE FALSE END
		 WHERE id = $1 RETURNING `+userCols, id, p.IsActive, p.IsAdmin, p.PasswordHash))
	if err != nil {
		return domain.User{}, mapWriteErr("update user", err)
	}
	return u, nil
}

const channelCols = `id, number, name, stream_path, is_active`

func scanChannel(row pgx.Row) (domain.Channel, error) {
	var c domain.Channel
	err := row.Scan(&c.ID, &c.Number, &c.Name, &c.StreamPath, &c.IsActive)
	return c, err
}

// ListChannels возвращает все каналы, включая выключенные, по возрастанию номера.
func (r *AdminRepository) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	rows, err := r.db.Query(ctx, `SELECT `+channelCols+` FROM channels ORDER BY number`)
	if err != nil {
		return nil, fmt.Errorf("query channels: %w", err)
	}
	defer rows.Close()
	var list []domain.Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channels: %w", err)
	}
	return list, nil
}

// CreateChannel добавляет канал. Занятый номер даёт domain.ErrConflict.
func (r *AdminRepository) CreateChannel(ctx context.Context, c domain.Channel) (domain.Channel, error) {
	out, err := scanChannel(r.db.QueryRow(ctx,
		`INSERT INTO channels (number, name, stream_path, is_active) VALUES ($1, $2, $3, $4)
		 RETURNING `+channelCols, c.Number, c.Name, c.StreamPath, c.IsActive))
	if err != nil {
		return domain.Channel{}, mapWriteErr("insert channel", err)
	}
	return out, nil
}

// UpdateChannel меняет заданные поля канала.
func (r *AdminRepository) UpdateChannel(ctx context.Context, id int64, p admin.ChannelPatch) (domain.Channel, error) {
	out, err := scanChannel(r.db.QueryRow(ctx,
		`UPDATE channels SET
		     number      = COALESCE($2::integer, number),
		     name        = COALESCE($3::text, name),
		     stream_path = COALESCE($4::text, stream_path),
		     is_active   = COALESCE($5::boolean, is_active)
		 WHERE id = $1 RETURNING `+channelCols, id, p.Number, p.Name, p.StreamPath, p.IsActive))
	if err != nil {
		return domain.Channel{}, mapWriteErr("update channel", err)
	}
	return out, nil
}

const packageSelect = `SELECT p.id, p.name, p.is_active,
	ARRAY(SELECT pc.channel_id FROM package_channels pc WHERE pc.package_id = p.id ORDER BY pc.channel_id)
	FROM packages p`

func scanPackage(row pgx.Row) (domain.Package, error) {
	var p domain.Package
	err := row.Scan(&p.ID, &p.Name, &p.IsActive, &p.ChannelIDs)
	return p, err
}

// ListPackages возвращает все пакеты с составом по названию.
func (r *AdminRepository) ListPackages(ctx context.Context) ([]domain.Package, error) {
	rows, err := r.db.Query(ctx, packageSelect+` ORDER BY p.name`)
	if err != nil {
		return nil, fmt.Errorf("query packages: %w", err)
	}
	defer rows.Close()
	var list []domain.Package
	for rows.Next() {
		p, err := scanPackage(rows)
		if err != nil {
			return nil, fmt.Errorf("scan package: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate packages: %w", err)
	}
	return list, nil
}

// GetPackage возвращает пакет по id или domain.ErrNotFound.
func (r *AdminRepository) GetPackage(ctx context.Context, id int64) (domain.Package, error) {
	p, err := scanPackage(r.db.QueryRow(ctx, packageSelect+` WHERE p.id = $1`, id))
	if err != nil {
		return domain.Package{}, mapWriteErr("query package", err)
	}
	return p, nil
}

// CreatePackage добавляет пустой пакет. Занятое имя даёт domain.ErrConflict.
func (r *AdminRepository) CreatePackage(ctx context.Context, p domain.Package) (domain.Package, error) {
	var out domain.Package
	err := r.db.QueryRow(ctx,
		`INSERT INTO packages (name, is_active) VALUES ($1, $2) RETURNING id, name, is_active`,
		p.Name, p.IsActive).Scan(&out.ID, &out.Name, &out.IsActive)
	if err != nil {
		return domain.Package{}, mapWriteErr("insert package", err)
	}
	out.ChannelIDs = []int64{}
	return out, nil
}

// UpdatePackage меняет имя или флаг пакета и возвращает его вместе с составом.
func (r *AdminRepository) UpdatePackage(ctx context.Context, id int64, p admin.PackagePatch) (domain.Package, error) {
	var upd int64
	err := r.db.QueryRow(ctx,
		`UPDATE packages SET
		     name      = COALESCE($2::text, name),
		     is_active = COALESCE($3::boolean, is_active)
		 WHERE id = $1 RETURNING id`, id, p.Name, p.IsActive).Scan(&upd)
	if err != nil {
		return domain.Package{}, mapWriteErr("update package", err)
	}
	return r.GetPackage(ctx, id)
}

// SetPackageChannels заменяет состав пакета. Вызывать нужно внутри транзакции:
// DELETE и INSERT должны применяться вместе.
func (r *AdminRepository) SetPackageChannels(ctx context.Context, packageID int64, channelIDs []int64) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM package_channels WHERE package_id = $1`, packageID); err != nil {
		return mapWriteErr("clear package channels", err)
	}
	if len(channelIDs) == 0 {
		return nil
	}
	if _, err := r.db.Exec(ctx,
		`INSERT INTO package_channels (package_id, channel_id) SELECT $1, unnest($2::bigint[])`,
		packageID, channelIDs); err != nil {
		return mapWriteErr("insert package channels", err)
	}
	return nil
}

const subCols = `id, user_id, package_id, is_active, expires_at, created_at`

func scanSubscription(row pgx.Row) (domain.UserSubscription, error) {
	var s domain.UserSubscription
	err := row.Scan(&s.ID, &s.UserID, &s.PackageID, &s.IsActive, &s.ExpiresAt, &s.CreatedAt)
	return s, err
}

// ListUserSubscriptions возвращает все подписки пользователя по id.
func (r *AdminRepository) ListUserSubscriptions(ctx context.Context, userID int64) ([]domain.UserSubscription, error) {
	rows, err := r.db.Query(ctx, `SELECT `+subCols+` FROM subscriptions WHERE user_id = $1 ORDER BY id`, userID)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions: %w", err)
	}
	defer rows.Close()
	var list []domain.UserSubscription
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}
	return list, nil
}

// CreateSubscription выдаёт пакет пользователю. Повтор даёт domain.ErrConflict.
func (r *AdminRepository) CreateSubscription(ctx context.Context, s domain.UserSubscription) (domain.UserSubscription, error) {
	out, err := scanSubscription(r.db.QueryRow(ctx,
		`INSERT INTO subscriptions (user_id, package_id, is_active, expires_at) VALUES ($1, $2, $3, $4)
		 RETURNING `+subCols, s.UserID, s.PackageID, s.IsActive, s.ExpiresAt))
	if err != nil {
		return domain.UserSubscription{}, mapWriteErr("insert subscription", err)
	}
	return out, nil
}

// UpdateSubscription меняет флаг и срок. $3 говорит, нужно ли трогать expires_at,
// потому что NULL в $4 означает «сделать бессрочной», а не «не менять».
func (r *AdminRepository) UpdateSubscription(ctx context.Context, id int64, p admin.SubscriptionPatch) (domain.UserSubscription, error) {
	out, err := scanSubscription(r.db.QueryRow(ctx,
		`UPDATE subscriptions SET
		     is_active  = COALESCE($2::boolean, is_active),
		     expires_at = CASE WHEN $3::boolean THEN $4::timestamptz ELSE expires_at END
		 WHERE id = $1 RETURNING `+subCols, id, p.IsActive, p.ExpiresAt.Set, p.ExpiresAt.Value))
	if err != nil {
		return domain.UserSubscription{}, mapWriteErr("update subscription", err)
	}
	return out, nil
}
