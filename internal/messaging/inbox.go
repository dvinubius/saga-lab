package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5"
)

const inboxSchema = `
CREATE TABLE IF NOT EXISTS inbox (
    message_id TEXT PRIMARY KEY,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

func Claim(ctx context.Context, tx pgx.Tx, msg *message.Message) (bool, error) {
	claimed, err := tx.Exec(ctx, `INSERT INTO inbox (message_id) VALUES ($1) ON CONFLICT DO NOTHING`, msg.UUID)
	if err != nil {
		return false, fmt.Errorf("claim message %s: %w", msg.UUID, err)
	}
	return claimed.RowsAffected() == 1, nil
}

func PruneInbox(ctx context.Context, db execer, retention time.Duration) error {
	if _, err := db.Exec(ctx, `DELETE FROM inbox WHERE received_at < $1`, time.Now().Add(-retention)); err != nil {
		return fmt.Errorf("prune inbox: %w", err)
	}
	return nil
}

func RunInboxPruning(ctx context.Context, db execer, retention, interval time.Duration, logger *slog.Logger) error {
	for {
		if err := PruneInbox(ctx, db, retention); err != nil && ctx.Err() == nil {
			logger.Error("prune inbox", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(interval):
		}
	}
}
