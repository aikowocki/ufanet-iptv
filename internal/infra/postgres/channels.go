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
