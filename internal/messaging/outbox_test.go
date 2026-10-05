package messaging_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/jackc/pgx/v5"
	amqp091 "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

func TestRelayRecordsOnlyItsSendSpans(t *testing.T) {
	amqpURL := os.Getenv("SAGAS_AMQP_URL")
	if amqpURL == "" {
		t.Skip("SAGAS_AMQP_URL is not set; run scripts/test.sh")
	}
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	id := strings.ReplaceAll(watermill.NewUUID(), "-", "")
	ctx := context.Background()
	db, err := postgres.Connect(ctx, pgtest.NewServiceDatabase(t, "relay_"+id))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	if err := messaging.Outbox.Create(ctx, db); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	topic := "RelaySpans_" + id
	broker, err := messaging.Connect(amqpURL, slog.New(slog.NewTextHandler(io.Discard, nil)), topic)
	if err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { broker.Close() })
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		msg, err := messaging.New(ctx, "transfer", messaging.DebitFunds{TransferID: "transfer", Amount: 25}, "")
		if err != nil {
			return err
		}
		return messaging.Enqueue(ctx, tx, topic, msg)
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	relayCtx, stop := context.WithCancel(ctx)
	relayed := make(chan error)
	go func() { relayed <- broker.RunRelay(relayCtx, db, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var pending int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&pending); err != nil {
			t.Fatalf("count outbox rows: %v", err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("outbox still holds %d rows after test deadline", pending)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	stop()
	if err := <-relayed; err != nil {
		t.Fatalf("relay: %v", err)
	}

	var names []string
	for _, s := range recorder.Ended() {
		names = append(names, s.Name())
	}
	if len(names) != 1 || names[0] != "send "+topic {
		t.Fatalf("recorded spans = %q, want only %q", names, "send "+topic)
	}
}

func TestRelayKeepsEarlierEntriesPublishedWhenAConfirmationHookFails(t *testing.T) {
	amqpURL := os.Getenv("SAGAS_AMQP_URL")
	if amqpURL == "" {
		t.Skip("SAGAS_AMQP_URL is not set; run scripts/test.sh")
	}
	id := strings.ReplaceAll(watermill.NewUUID(), "-", "")
	ctx := context.Background()
	db, err := postgres.Connect(ctx, pgtest.NewServiceDatabase(t, "relay_"+id))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)
	if err := messaging.Outbox.Create(ctx, db); err != nil {
		t.Fatalf("create tables: %v", err)
	}
	plain, hooked := "RelayPlain_"+id, "RelayHooked_"+id
	broker, err := messaging.Connect(amqpURL, slog.New(slog.NewTextHandler(io.Discard, nil)), plain, hooked)
	if err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { broker.Close() })
	hookCalls := 0
	broker.OnConfirmed(hooked, func(ctx context.Context, tx pgx.Tx, msg *message.Message, confirmedAt time.Time) error {
		hookCalls++
		if hookCalls == 1 {
			return errors.New("hook failed")
		}
		return nil
	})
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		for _, topic := range []string{plain, hooked} {
			msg, err := messaging.New(ctx, "transfer", messaging.DebitFunds{TransferID: "transfer", Amount: 25}, "")
			if err != nil {
				return err
			}
			if err := messaging.Enqueue(ctx, tx, topic, msg); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	relayCtx, stop := context.WithCancel(ctx)
	relayed := make(chan error)
	go func() { relayed <- broker.RunRelay(relayCtx, db, slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var pending int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox`).Scan(&pending); err != nil {
			t.Fatalf("count outbox rows: %v", err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("outbox still holds %d rows after test deadline", pending)
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	if err := <-relayed; err != nil {
		t.Fatalf("relay: %v", err)
	}

	conn, err := amqp091.Dial(amqpURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	channel, err := conn.Channel()
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	for topic, want := range map[string]int{plain: 1, hooked: 2} {
		queue, err := messaging.DeclareQueue(channel, topic)
		if err != nil {
			t.Fatal(err)
		}
		if queue.Messages != want {
			t.Errorf("%s messages = %d, want %d", topic, queue.Messages, want)
		}
	}
	if hookCalls != 2 {
		t.Errorf("hook calls = %d, want 2", hookCalls)
	}
}
