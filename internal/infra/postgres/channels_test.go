package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/aikowocki/ufanet-iptv/internal/domain"
)

// Интеграционный тест: пропускается без TEST_DATABASE_URL.
func TestChannelRepository(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewChannelRepository(NewExecutor(pool))

	if _, err := pool.Exec(ctx, `INSERT INTO channels (number, name, stream_path, is_active) VALUES (2,'B','/b',true),(1,'A','/a',true),(3,'C','/c',false)`); err != nil {
		t.Fatal(err)
	}

	list, err := repo.ListActiveChannels(ctx)
	if err != nil || len(list) != 2 || list[0].Number != 1 || list[1].Number != 2 {
		t.Fatalf("ListActiveChannels = %+v, %v", list, err)
	}

	ch, err := repo.GetChannel(ctx, list[0].ID)
	if err != nil || ch.Name != "A" {
		t.Fatalf("GetChannel = %+v, %v", ch, err)
	}
	if _, err := repo.GetChannel(ctx, 1<<40); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing channel err = %v, want ErrNotFound", err)
	}
}
