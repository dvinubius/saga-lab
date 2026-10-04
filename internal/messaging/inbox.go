package messaging

import (
	"context"
	"fmt"

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
