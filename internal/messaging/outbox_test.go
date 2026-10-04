package messaging_test

import (
	"context"
	"testing"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/jackc/pgx/v5"
)

func TestRecreateEmptiesTheInboxAndOutbox(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	if err := messaging.InboxAndOutbox.Create(ctx, db); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for range 2 {
			msg, err := messaging.New(ctx, "transfer", messaging.DebitFunds{TransferID: "transfer", Amount: 25}, "")
			if err != nil {
				return err
			}
			if _, err := messaging.Claim(ctx, tx, msg); err != nil {
				return err
			}
			if err := messaging.Enqueue(ctx, tx, messaging.DebitFundsTopic, msg); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed inbox and outbox: %v", err)
	}

	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error { return messaging.InboxAndOutbox.Recreate(ctx, tx) }); err != nil {
		t.Fatalf("recreate tables: %v", err)
	}

	for _, table := range []string{"inbox", "outbox"} {
		var rows int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&rows); err != nil {
			t.Fatalf("count %s rows: %v", table, err)
		}
		if rows != 0 {
			t.Fatalf("%s rows = %d, want none", table, rows)
		}
	}
}

func TestOutboxTablesHaveNoInbox(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	if err := messaging.Outbox.Create(ctx, db); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	if err := pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error { return messaging.Outbox.Recreate(ctx, tx) }); err != nil {
		t.Fatalf("recreate tables: %v", err)
	}

	var inbox, outbox bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('inbox') IS NOT NULL, to_regclass('outbox') IS NOT NULL`).Scan(&inbox, &outbox); err != nil {
		t.Fatalf("look up tables: %v", err)
	}
	if inbox || !outbox {
		t.Fatalf("inbox exists = %v, outbox exists = %v, want only the outbox", inbox, outbox)
	}
}
