package bank

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/service"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/errgroup"
)

//go:embed schema.sql
var schema string

var errInjectedFailure = errors.New("injected lost acknowledgement after commit")

type Role struct {
	service         string
	commands        []command
	publishedTopics []string
}

type command struct {
	topic   string
	execute func(*Bank, *message.Message) error
}

var (
	Source = Role{messaging.BankA,
		[]command{{messaging.DebitFundsTopic, (*Bank).debitFunds}, {messaging.RefundFundsTopic, (*Bank).refundFunds}},
		[]string{messaging.FundsDebitedTopic, messaging.DebitRejectedTopic, messaging.FundsRefundedTopic, messaging.ProcessingObservedTopic}}
	Destination = Role{messaging.BankB,
		[]command{{messaging.CreditFundsTopic, (*Bank).creditFunds}, {messaging.ResumeDeliveryTopic, (*Bank).resumeDelivery}},
		[]string{messaging.FundsCreditedTopic, messaging.CreditRejectedTopic, messaging.ProcessingObservedTopic}}
)

type Config struct {
	OpeningBalance    int64
	TopUpAmount       int64
	Role              Role
	DedicatedConsumer DedicatedConsumer
}

type Bank struct {
	db             *pgxpool.Pool
	openingBalance int64
	topUpAmount    int64
	role           Role
	broker         *messaging.Broker
	logger         *slog.Logger
	dedicated      DedicatedConsumer
}

func Run(ctx context.Context, settings service.Settings, config Config) error {
	defer settings.Listener.Close()
	db, err := postgres.Connect(ctx, settings.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	b, err := Open(ctx, db, config, settings.Logger)
	if err != nil {
		return err
	}
	broker, err := messaging.Connect(settings.AMQPURL, settings.Logger, config.Role.publishedTopics...)
	if err != nil {
		return err
	}
	defer broker.Close()
	b.attachBroker(broker)
	var dedicated *amqpConsumer
	if config.Role.service == messaging.BankB {
		dedicated, err = openDedicated(settings.AMQPURL)
		if err != nil {
			return err
		}
		defer dedicated.connection.Close()
		b.dedicated = dedicated
	}

	g, ctx := errgroup.WithContext(ctx)
	if dedicated != nil {
		g.Go(func() error { dedicated.run(ctx, b); return nil })
	}
	g.Go(func() error { return broker.Run(ctx) })
	g.Go(func() error { return broker.RunRelay(ctx, db, settings.Logger) })
	g.Go(func() error { return web.Serve(ctx, settings.Listener, b.Handler(), web.Internal, settings.Logger) })
	return g.Wait()
}

func Open(ctx context.Context, db *pgxpool.Pool, config Config, logger *slog.Logger) (*Bank, error) {
	if err := applySchema(ctx, db); err != nil {
		return nil, err
	}
	if err := messaging.InboxAndOutbox.Create(ctx, db); err != nil {
		return nil, err
	}
	return &Bank{db: db, role: config.Role, openingBalance: config.OpeningBalance, topUpAmount: config.TopUpAmount, logger: logger, dedicated: config.DedicatedConsumer}, nil
}

func Reset(ctx context.Context, settings service.Settings, config Config) error {
	var commandTopics []string
	for _, c := range config.Role.commands {
		commandTopics = append(commandTopics, c.topic)
	}
	if config.Role.service == messaging.BankB {
		commandTopics = append(commandTopics, messaging.CreditFundsDedicatedTopic)
	}
	if err := messaging.Purge(settings.AMQPURL, commandTopics...); err != nil {
		return err
	}
	db, err := postgres.Connect(ctx, settings.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS accounts`); err != nil {
			return fmt.Errorf("drop accounts: %w", err)
		}
		if err := messaging.InboxAndOutbox.Recreate(ctx, tx); err != nil {
			return err
		}
		return applySchema(ctx, tx)
	})
}

type execer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func applySchema(ctx context.Context, db execer) error {
	if _, err := db.Exec(ctx, schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func (b *Bank) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /accounts/{visitorID}", b.getAccount)
	mux.HandleFunc("PUT /accounts/{visitorID}", b.openAccount)
	if b.topUpAmount > 0 {
		mux.HandleFunc("POST /accounts/{visitorID}/top-ups", b.topUp)
	}
	mux.Handle("GET /readyz", web.Readiness(b.ready, b.logger))
	return mux
}

func (b *Bank) ready(ctx context.Context) error {
	if b.broker != nil {
		if err := b.broker.Ready(); err != nil {
			return err
		}
	}
	return b.db.Ping(ctx)
}

func (b *Bank) attachBroker(broker *messaging.Broker) {
	b.broker = broker
	for _, c := range b.role.commands {
		broker.Router.AddConsumerHandler(c.topic, c.topic, broker.Subscriber, func(msg *message.Message) error {
			return c.execute(b, msg)
		})
	}
}

func (b *Bank) debitFunds(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), b.logger)
	var command messaging.DebitFunds
	if err := messaging.Decode(msg, &command); err != nil {
		logger.Error("discard command", "error", err)
		return nil
	}
	if command.Amount <= 0 {
		logger.Error("discard debit with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil
	}
	debited := false
	err := b.apply(msg, messaging.DebitFundsTopic, command.TransferID, func(ctx context.Context, tx pgx.Tx) error {
		var balance int64
		if err := tx.QueryRow(ctx,
			`SELECT balance FROM accounts WHERE visitor_id = $1 FOR UPDATE`, command.VisitorID,
		).Scan(&balance); err != nil {
			return err
		}
		if balance < command.Amount {
			return enqueue(tx, command.TransferID, messaging.DebitRejectedTopic, messaging.DebitRejected{
				TransferID: command.TransferID, Reason: "Insufficient funds", ObservedAt: time.Now(),
			}, msg)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE accounts SET balance = balance - $2 WHERE visitor_id = $1`,
			command.VisitorID, command.Amount,
		); err != nil {
			return err
		}
		debited = true
		return enqueue(tx, command.TransferID, messaging.FundsDebitedTopic, messaging.FundsDebited{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
	}, logger)
	return b.loseAcknowledgementAfterCommit(msg, messaging.ScenarioOperation(command), messaging.DebitRedelivery, debited, err, logger)
}

func (b *Bank) loseAcknowledgementAfterCommit(msg *message.Message, command messaging.ScenarioOperation, injectedUnder string, applied bool, err error, logger *slog.Logger) error {
	if err != nil || !applied || command.Scenario != injectedUnder {
		return err
	}
	trace.SpanFromContext(msg.Context()).AddEvent("fault.injected")
	logger.Warn("simulating lost acknowledgement after commit; Nack (requeue) requested", "transfer_id", command.TransferID, "message_id", msg.UUID)
	if err := pgx.BeginFunc(msg.Context(), b.db, func(tx pgx.Tx) error {
		return b.observe(tx, command.TransferID, messaging.NackRequested, msg)
	}); err != nil {
		logger.Error("record NackRequested", "transfer_id", command.TransferID, "message_id", msg.UUID, "error", err)
	}
	return errInjectedFailure
}

func (b *Bank) creditFunds(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), b.logger)
	var command messaging.CreditFunds
	if err := messaging.Decode(msg, &command); err != nil {
		logger.Error("discard command", "error", err)
		return nil
	}
	if command.Amount <= 0 {
		logger.Error("discard credit with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil
	}
	rejected := false
	err := b.apply(msg, messaging.CreditFundsTopic, command.TransferID, func(ctx context.Context, tx pgx.Tx) error {
		if command.Scenario == messaging.CreditRejection || command.Scenario == messaging.RefundRedelivery {
			rejected = true
			return enqueue(tx, command.TransferID, messaging.CreditRejectedTopic, messaging.CreditRejected{
				TransferID: command.TransferID, Reason: "Credit refused by Bank B", ObservedAt: time.Now(),
			}, msg)
		}
		if err := addFunds(ctx, tx, command.VisitorID, command.Amount); err != nil {
			return err
		}
		return enqueue(tx, command.TransferID, messaging.FundsCreditedTopic, messaging.FundsCredited{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
	}, logger)
	if err == nil && rejected {
		logger.Info("credit rejected as the scenario requires", "transfer_id", command.TransferID, "message_id", msg.UUID)
	}
	return err
}

func (b *Bank) refundFunds(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), b.logger)
	var command messaging.RefundFunds
	if err := messaging.Decode(msg, &command); err != nil {
		logger.Error("discard command", "error", err)
		return nil
	}
	if command.Amount <= 0 {
		logger.Error("discard refund with non-positive amount", "transfer_id", command.TransferID, "amount", command.Amount)
		return nil
	}
	refunded := false
	err := b.apply(msg, messaging.RefundFundsTopic, command.TransferID, func(ctx context.Context, tx pgx.Tx) error {
		if err := addFunds(ctx, tx, command.VisitorID, command.Amount); err != nil {
			return err
		}
		refunded = true
		return enqueue(tx, command.TransferID, messaging.FundsRefundedTopic, messaging.FundsRefunded{TransferID: command.TransferID, ObservedAt: time.Now()}, msg)
	}, logger)
	return b.loseAcknowledgementAfterCommit(msg, messaging.ScenarioOperation(command), messaging.RefundRedelivery, refunded, err, logger)
}

func addFunds(ctx context.Context, tx pgx.Tx, visitorID string, amount int64) error {
	result, err := tx.Exec(ctx,
		`UPDATE accounts SET balance = balance + $2 WHERE visitor_id = $1`,
		visitorID, amount,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (b *Bank) apply(msg *message.Message, topic, transferID string, effect func(context.Context, pgx.Tx) error, logger *slog.Logger) error {
	ctx := msg.Context()
	duplicate := false
	err := pgx.BeginFunc(ctx, b.db, func(tx pgx.Tx) error {
		claimed, err := messaging.Claim(ctx, tx, msg)
		if err != nil {
			return err
		}
		if !claimed {
			duplicate = true
			return b.observe(tx, transferID, messaging.DuplicateSuppressed, msg)
		}
		return effect(ctx, tx)
	})
	if errors.Is(err, pgx.ErrNoRows) && topic != messaging.ResumeDeliveryTopic {
		logger.Warn("command not applied: account missing", "command", topic, "transfer_id", transferID, "message_id", msg.UUID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s for transfer %s: %w", topic, transferID, err)
	}
	if duplicate {
		trace.SpanFromContext(ctx).AddEvent("duplicate.suppressed")
		logger.Info("ignore duplicate command", "command", topic, "transfer_id", transferID, "message_id", msg.UUID)
	}
	return nil
}

func (b *Bank) observe(tx pgx.Tx, transferID, observation string, command *message.Message) error {
	return enqueue(tx, transferID, messaging.ProcessingObservedTopic, messaging.ProcessingObserved{
		TransferID: transferID, Observation: observation, Service: b.role.service,
		AttemptID: messaging.AttemptID(command.Context()), ObservedAt: time.Now(),
	}, command)
}

func enqueue(tx pgx.Tx, transferID, topic string, event any, command *message.Message) error {
	msg, err := messaging.New(command.Context(), transferID, event, command.UUID)
	if err != nil {
		return err
	}
	return messaging.Enqueue(command.Context(), tx, topic, msg)
}

type account struct {
	VisitorID string `json:"visitor_id"`
	Balance   int64  `json:"balance"`
}

func (b *Bank) getAccount(w http.ResponseWriter, r *http.Request) {
	a := account{VisitorID: r.PathValue("visitorID")}
	err := b.db.QueryRow(r.Context(),
		`SELECT balance FROM accounts WHERE visitor_id = $1`, a.VisitorID,
	).Scan(&a.Balance)
	if errors.Is(err, pgx.ErrNoRows) {
		web.WriteError(w, http.StatusNotFound, "account not found", b.logger)
		return
	}
	if err != nil {
		b.logger.Error("read account", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "account unavailable", b.logger)
		return
	}
	web.WriteJSON(w, http.StatusOK, a, b.logger)
}

func (b *Bank) openAccount(w http.ResponseWriter, r *http.Request) {
	if _, err := b.db.Exec(r.Context(), `INSERT INTO accounts (visitor_id, balance) VALUES ($1, $2) ON CONFLICT (visitor_id) DO NOTHING`, r.PathValue("visitorID"), b.openingBalance); err != nil {
		b.logger.Error("open account", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "account unavailable", b.logger)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b *Bank) topUp(w http.ResponseWriter, r *http.Request) {
	a := account{VisitorID: r.PathValue("visitorID")}
	err := b.db.QueryRow(r.Context(),
		`UPDATE accounts SET balance = balance + $2 WHERE visitor_id = $1 RETURNING balance`,
		a.VisitorID, b.topUpAmount,
	).Scan(&a.Balance)
	if errors.Is(err, pgx.ErrNoRows) {
		web.WriteError(w, http.StatusNotFound, "account not found", b.logger)
		return
	}
	if err != nil {
		b.logger.Error("top up account", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "account unavailable", b.logger)
		return
	}
	web.WriteJSON(w, http.StatusOK, a, b.logger)
}
