package messaging

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	relayInterval  = 100 * time.Millisecond
	relayMaxDelay  = time.Second
	relayBatchSize = 100
)

const outboxSchema = `
CREATE TABLE IF NOT EXISTS outbox (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    topic TEXT NOT NULL,
    message_id TEXT NOT NULL,
    payload BYTEA NOT NULL,
    metadata JSONB NOT NULL
)`

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type Tables struct {
	inbox bool
}

var (
	Outbox         = Tables{}
	InboxAndOutbox = Tables{inbox: true}
)

func (t Tables) Create(ctx context.Context, db execer) error {
	if t.inbox {
		if _, err := db.Exec(ctx, inboxSchema); err != nil {
			return fmt.Errorf("create inbox: %w", err)
		}
	}
	if _, err := db.Exec(ctx, outboxSchema); err != nil {
		return fmt.Errorf("create outbox: %w", err)
	}
	return nil
}

func (t Tables) Recreate(ctx context.Context, tx pgx.Tx) error {
	drop := `DROP TABLE IF EXISTS outbox`
	if t.inbox {
		drop += `, inbox`
	}
	if _, err := tx.Exec(ctx, drop); err != nil {
		return fmt.Errorf("drop messaging tables: %w", err)
	}
	return t.Create(ctx, tx)
}

func Enqueue(ctx context.Context, tx pgx.Tx, topic string, msg *message.Message) error {
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.Metadata))
	if attemptID := AttemptID(ctx); attemptID != "" {
		msg.Metadata.Set(attemptIDMetadataKey, attemptID)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox (topic, message_id, payload, metadata) VALUES ($1, $2, $3, $4)`,
		topic, msg.UUID, msg.Payload, msg.Metadata,
	); err != nil {
		return fmt.Errorf("enqueue %s message %s: %w", topic, msg.UUID, err)
	}
	return nil
}

type outboxEntry struct {
	ID        int64
	Topic     string
	MessageID string
	Payload   []byte
	Metadata  message.Metadata
}

type ConfirmationHook func(context.Context, pgx.Tx, *message.Message, time.Time) error

func (b *Broker) OnConfirmed(topic string, hook ConfirmationHook) {
	if b.confirmationHooks == nil {
		b.confirmationHooks = map[string]ConfirmationHook{}
	}
	b.confirmationHooks[topic] = hook
}

func (b *Broker) RunRelay(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) error {
	failures := 0
	for {
		delay := relayInterval
		published, failed, err := b.relay(ctx, db)
		if ctx.Err() != nil {
			return nil
		}
		if published > 0 && failures > 0 {
			logger.Info("outbox relay resumed", "failures", failures)
			failures = 0
		}
		if err != nil {
			failures++
			attributes := []any{"error", err, "failures", failures}
			if failed != nil {
				attributes = append(attributes, "topic", failed.Topic, "message_id", failed.MessageID, "transfer_id", failed.Metadata.Get(transferIDKey))
			}
			logger.Warn("outbox relay failed", attributes...)
			delay = min(relayInterval<<min(failures, 4), relayMaxDelay)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (b *Broker) relay(ctx context.Context, db *pgxpool.Pool) (int, *outboxEntry, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id, topic, message_id, payload, metadata FROM outbox ORDER BY id LIMIT $1 FOR UPDATE`, relayBatchSize)
	if err != nil {
		return 0, nil, err
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[outboxEntry])
	if err != nil {
		return 0, nil, err
	}
	published := 0
	var failed *outboxEntry
	var entryErr error
	for _, entry := range entries {
		if entryErr = b.forward(ctx, entry); entryErr != nil {
			failed = &entry
			break
		}
		confirmedAt := time.Now()
		if hook := b.confirmationHooks[entry.Topic]; hook != nil {
			msg := message.NewMessage(entry.MessageID, entry.Payload)
			msg.Metadata = entry.Metadata
			msg.SetContext(ctx)
			if err := pgx.BeginFunc(ctx, tx, func(savepoint pgx.Tx) error { return hook(ctx, savepoint, msg, confirmedAt) }); err != nil {
				failed = &entry
				entryErr = fmt.Errorf("record confirmed %s message %s: %w", entry.Topic, entry.MessageID, err)
				break
			}
		}
		published++
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, entry.ID); err != nil {
			return published, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return published, nil, err
	}
	return published, failed, entryErr
}

func (b *Broker) forward(ctx context.Context, entry outboxEntry) error {
	msg := message.NewMessage(entry.MessageID, entry.Payload)
	msg.Metadata = entry.Metadata
	msg.SetContext(ctx)
	enqueued := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(entry.Metadata))
	return send(enqueued, b.amqpPublisher, entry.Topic, msg)
}
