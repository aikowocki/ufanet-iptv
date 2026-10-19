package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// ChannelRepository читает каналы. Все запросы параметризованы.
type ChannelRepository struct{ db Querier }

// NewChannelRepository создаёт ChannelRepository.
func NewChannelRepository(db Querier) *ChannelRepository { return &ChannelRepository{db: db} }

// ListActiveChannels возвращает включённые каналы по возрастанию номера.
func (r *ChannelRepository) ListActiveChannels(ctx context.Context) ([]domain.Channel, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, number, name, stream_path, is_active FROM channels WHERE is_active ORDER BY number`)
	if err != nil {
		return nil, fmt.Errorf("query channels: %w", err)
	}
	defer rows.Close()
	var list []domain.Channel
	for rows.Next() {
		var c domain.Channel
		if err := rows.Scan(&c.ID, &c.Number, &c.Name, &c.StreamPath, &c.IsActive); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channels: %w", err)
	}
	return list, nil
}

// Условие действующей подписки на канал c. Время берётся из now() на стороне БД,
// поэтому все экземпляры сервиса считают срок по одним часам. $1 — id пользователя.
const subscribed = `EXISTS (
	SELECT 1
	FROM package_channels pc
	JOIN packages p ON p.id = pc.package_id AND p.is_active
	JOIN subscriptions s ON s.package_id = p.id
	WHERE pc.channel_id = c.id
	  AND s.user_id = $1
	  AND s.is_active
	  AND (s.expires_at IS NULL OR s.expires_at > now()))`

// ListChannelsForUser возвращает все включённые каналы по возрастанию номера
// с признаком доступности по подписке пользователя.
func (r *ChannelRepository) ListChannelsForUser(ctx context.Context, userID int64) ([]domain.ChannelAccess, error) {
	rows, err := r.db.Query(ctx,
		`SELECT c.id, c.number, c.name, c.stream_path, c.is_active, `+subscribed+`
		 FROM channels c WHERE c.is_active ORDER BY c.number`, userID)
	if err != nil {
		return nil, fmt.Errorf("query channels for user: %w", err)
	}
	defer rows.Close()
	var list []domain.ChannelAccess
	for rows.Next() {
		var c domain.ChannelAccess
		if err := rows.Scan(&c.ID, &c.Number, &c.Name, &c.StreamPath, &c.IsActive, &c.Available); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate channels: %w", err)
	}
	return list, nil
}

// HasChannelAccess сообщает, есть ли у пользователя действующая подписка на включённый канал.
func (r *ChannelRepository) HasChannelAccess(ctx context.Context, userID, channelID int64) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM channels c WHERE c.id = $2 AND c.is_active AND `+subscribed+`)`,
		userID, channelID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("query channel access: %w", err)
	}
	return ok, nil
}

// GetChannel возвращает канал по id (в том числе выключенный) или domain.ErrNotFound.
func (r *ChannelRepository) GetChannel(ctx context.Context, id int64) (domain.Channel, error) {
	var c domain.Channel
	err := r.db.QueryRow(ctx,
		`SELECT id, number, name, stream_path, is_active FROM channels WHERE id = $1`, id,
	).Scan(&c.ID, &c.Number, &c.Name, &c.StreamPath, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Channel{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Channel{}, fmt.Errorf("query channel: %w", err)
	}
	return c, nil
}

// GetChannelByStreamPath возвращает включённый канал по stream_path или domain.ErrNotFound.
// Выключенный канал считается несуществующим.
func (r *ChannelRepository) GetChannelByStreamPath(ctx context.Context, streamPath string) (domain.Channel, error) {
	var c domain.Channel
	err := r.db.QueryRow(ctx,
		`SELECT id, number, name, stream_path, is_active FROM channels WHERE stream_path = $1 AND is_active`, streamPath,
	).Scan(&c.ID, &c.Number, &c.Name, &c.StreamPath, &c.IsActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Channel{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Channel{}, fmt.Errorf("query channel by stream path: %w", err)
	}
	return c, nil
}
