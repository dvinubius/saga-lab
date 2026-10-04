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

func CreateTables(ctx context.Context, db execer) error {
	if _, err := db.Exec(ctx, inboxSchema); err != nil {
		return fmt.Errorf("create inbox: %w", err)
	}
	if _, err := db.Exec(ctx, outboxSchema); err != nil {
		return fmt.Errorf("create outbox: %w", err)
	}
	return nil
}

func RecreateTables(ctx context.Context, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS inbox, outbox`); err != nil {
		return fmt.Errorf("drop inbox and outbox: %w", err)
	}
	return CreateTables(ctx, tx)
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

func (b *Broker) RunRelay(ctx context.Context, db *pgxpool.Pool, logger *slog.Logger) error {
	failures := 0
	for {
		delay := relayInterval
		failed, err := b.relay(ctx, db)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			failures++
			attributes := []any{"error", err, "failures", failures}
			if failed != nil {
				attributes = append(attributes, "topic", failed.Topic, "message_id", failed.MessageID, "transfer_id", failed.Metadata.Get(transferIDKey))
			}
			logger.Warn("outbox relay failed", attributes...)
			delay = min(relayInterval<<min(failures, 4), relayMaxDelay)
		} else if failures > 0 {
			logger.Info("outbox relay resumed", "failures", failures)
			failures = 0
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (b *Broker) relay(ctx context.Context, db *pgxpool.Pool) (*outboxEntry, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id, topic, message_id, payload, metadata FROM outbox ORDER BY id LIMIT $1 FOR UPDATE`, relayBatchSize)
	if err != nil {
		return nil, err
	}
	entries, err := pgx.CollectRows(rows, pgx.RowToStructByPos[outboxEntry])
	if err != nil {
		return nil, err
	}
	var failed *outboxEntry
	var publishErr error
	for _, entry := range entries {
		if publishErr = b.forward(ctx, entry); publishErr != nil {
			failed = &entry
			break
		}
		if _, err := tx.Exec(ctx, `DELETE FROM outbox WHERE id = $1`, entry.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return failed, publishErr
}

func (b *Broker) forward(ctx context.Context, entry outboxEntry) error {
	msg := message.NewMessage(entry.MessageID, entry.Payload)
	msg.Metadata = entry.Metadata
	msg.SetContext(ctx)
	enqueued := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(entry.Metadata))
	return send(enqueued, b.amqpPublisher, entry.Topic, msg)
}
