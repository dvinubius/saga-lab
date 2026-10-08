package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPruningForgetsOnlyInboxEntriesOlderThanTheRetentionAge(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	if err := messaging.InboxAndOutbox.Create(ctx, db); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	old, recent := message.NewMessage("old", nil), message.NewMessage("recent", nil)
	for _, msg := range []*message.Message{old, recent} {
		if !claim(t, db, msg) {
			t.Fatalf("first claim of %s refused", msg.UUID)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE inbox SET received_at = now() - interval '8 days' WHERE message_id = $1`, old.UUID); err != nil {
		t.Fatalf("age inbox entry: %v", err)
	}

	if err := messaging.PruneInbox(ctx, db, 7*24*time.Hour); err != nil {
		t.Fatalf("prune inbox: %v", err)
	}

	if !claim(t, db, old) {
		t.Error("entry older than the retention age was kept")
	}
	if claim(t, db, recent) {
		t.Error("message with a remaining entry was not recognised as a duplicate")
	}
}

func claim(t *testing.T, db *pgxpool.Pool, msg *message.Message) bool {
	t.Helper()
	var claimed bool
	err := pgx.BeginFunc(context.Background(), db, func(tx pgx.Tx) error {
		var err error
		claimed, err = messaging.Claim(context.Background(), tx, msg)
		return err
	})
	if err != nil {
		t.Fatalf("claim %s: %v", msg.UUID, err)
	}
	return claimed
}
