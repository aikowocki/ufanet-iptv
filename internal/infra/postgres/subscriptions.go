package postgres

import (
	"context"
	"fmt"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// SubscriptionRepository читает подписки. Все запросы параметризованы.
type SubscriptionRepository struct{ db Querier }

// NewSubscriptionRepository создаёт SubscriptionRepository.
func NewSubscriptionRepository(db Querier) *SubscriptionRepository {
	return &SubscriptionRepository{db: db}
}

// ListUserSubscriptions возвращает все подписки пользователя (и действующие, и нет)
// по названию пакета. Статус считается на стороне БД по её now(), как и доступ к каналам.
func (r *SubscriptionRepository) ListUserSubscriptions(ctx context.Context, userID int64) ([]domain.Subscription, error) {
	rows, err := r.db.Query(ctx,
		`SELECT p.id, p.name, s.expires_at,
		        CASE WHEN NOT s.is_active OR NOT p.is_active THEN 'disabled'
		             WHEN s.expires_at IS NOT NULL AND s.expires_at <= now() THEN 'expired'
		             ELSE 'active' END
		 FROM subscriptions s JOIN packages p ON p.id = s.package_id
		 WHERE s.user_id = $1 ORDER BY p.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("query subscriptions: %w", err)
	}
	defer rows.Close()
	var list []domain.Subscription
	for rows.Next() {
		var s domain.Subscription
		if err := rows.Scan(&s.PackageID, &s.PackageName, &s.ExpiresAt, &s.Status); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate subscriptions: %w", err)
	}
	return list, nil
}
