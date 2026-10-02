package transferservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

const (
	transferServiceName = "Transfer Service"
	bankAName           = "Bank A"
	bankBName           = "Bank B"
)

type status string

const (
	debitPending  status = "debit_pending"
	creditPending status = "credit_pending"
	completed     status = "completed"
	rejected      status = "rejected"
)

func (s status) Pending() bool {
	return s == debitPending || s == creditPending
}

func (s status) Label() string {
	switch s {
	case debitPending:
		return "Waiting for Bank A to debit"
	case creditPending:
		return "Waiting for Bank B to credit"
	case completed:
		return "Completed"
	case rejected:
		return "Rejected by Bank A"
	}
	return string(s)
}

type step string

const (
	requested       step = "requested"
	debitCommitted  step = "debit_committed"
	debitRejected   step = "debit_rejected"
	creditCommitted step = "credit_committed"
	finished        step = "finished"
)

func (s step) Label() string {
	switch s {
	case requested:
		return "Transfer requested"
	case debitCommitted:
		return "Bank A committed the debit"
	case debitRejected:
		return "Bank A rejected the debit"
	case creditCommitted:
		return "Bank B committed the credit"
	case finished:
		return "Transfer completed"
	}
	return string(s)
}

type transfer struct {
	ID              string         `json:"transfer_id"`
	Amount          int64          `json:"amount"`
	Status          status         `json:"status"`
	RejectionReason string         `json:"rejection_reason,omitempty"`
	TraceID         string         `json:"trace_id,omitempty"`
	RequestedAt     time.Time      `json:"requested_at"`
	History         []historyEntry `json:"history,omitempty"`
}

type historyEntry struct {
	Step            step      `json:"step"`
	Service         string    `json:"service"`
	ObservedAt      time.Time `json:"observed_at"`
	RecordedAt      time.Time `json:"recorded_at"`
	MessageID       string    `json:"message_id,omitempty"`
	CausationID     string    `json:"causation_id,omitempty"`
	IssuedMessageID string    `json:"issued_message_id,omitempty"`
}

var errTransferNotFound = errors.New("transfer not found")

type pendingTransferError struct {
	PendingID string
}

func (e pendingTransferError) Error() string {
	return "transfer " + e.PendingID + " is still pending"
}

func (s *Service) submit(ctx context.Context, amount int64) (transfer, error) {
	t := transfer{ID: watermill.NewUUID(), Amount: amount, Status: debitPending, RequestedAt: time.Now()}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		t.TraceID = span.TraceID().String()
	}
	debit, err := messaging.New(ctx, t.ID, messaging.DebitFunds{TransferID: t.ID, VisitorID: visitor.PreparedID, Amount: amount}, "")
	if err != nil {
		return transfer{}, err
	}
	for {
		admitted, err := s.admit(ctx, t, debit.UUID)
		if err != nil {
			return transfer{}, fmt.Errorf("record transfer: %w", err)
		}
		if admitted {
			break
		}
		// The conflicting transfer may have ended before this lookup; then admission is tried again.
		pending, err := s.pendingTransferID(ctx)
		if err != nil {
			return transfer{}, fmt.Errorf("find pending transfer: %w", err)
		}
		if pending != "" {
			return transfer{}, pendingTransferError{PendingID: pending}
		}
	}
	if err := s.broker.Publisher.Publish(messaging.DebitFundsTopic, debit); err != nil {
		return transfer{}, fmt.Errorf("send DebitFunds for transfer %s: %w", t.ID, err)
	}
	return s.find(ctx, t.ID)
}

func (s *Service) admit(ctx context.Context, t transfer, debitID string) (bool, error) {
	admitted := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		inserted, err := tx.Exec(ctx,
			`INSERT INTO transfers (transfer_id, visitor_id, amount, status, requested_at, trace_id) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''))
			 ON CONFLICT (visitor_id) WHERE status IN ('debit_pending', 'credit_pending') DO NOTHING`,
			t.ID, visitor.PreparedID, t.Amount, t.Status, t.RequestedAt, t.TraceID,
		)
		if err != nil || inserted.RowsAffected() == 0 {
			return err
		}
		admitted = true
		return record(ctx, tx, t.ID, historyEntry{
			Step: requested, Service: transferServiceName, ObservedAt: t.RequestedAt, IssuedMessageID: debitID,
		})
	})
	return admitted, err
}

func (s *Service) pendingTransferID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`SELECT transfer_id FROM transfers WHERE visitor_id = $1 AND status IN ($2, $3)`,
		visitor.PreparedID, debitPending, creditPending,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Service) fundsDebited(msg *message.Message) ([]*message.Message, error) {
	var event messaging.FundsDebited
	if err := messaging.Decode(msg, &event); err != nil {
		slog.Error("discard event", "error", err)
		return nil, nil
	}
	ctx := msg.Context()
	var command *message.Message
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		credit := messaging.CreditFunds{TransferID: event.TransferID}
		err := tx.QueryRow(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3 RETURNING visitor_id, amount`,
			event.TransferID, creditPending, debitPending,
		).Scan(&credit.VisitorID, &credit.Amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if command, err = messaging.New(ctx, event.TransferID, credit, msg.UUID); err != nil {
			return err
		}
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: debitCommitted, Service: bankAName, ObservedAt: event.ObservedAt,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), IssuedMessageID: command.UUID,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("record debit for transfer %s: %w", event.TransferID, err)
	}
	if command == nil {
		slog.Warn("ignore FundsDebited for transfer not awaiting a debit", "transfer_id", event.TransferID, "message_id", msg.UUID)
		return nil, nil
	}
	return []*message.Message{command}, nil
}

func (s *Service) debitRejected(msg *message.Message) error {
	var event messaging.DebitRejected
	if err := messaging.Decode(msg, &event); err != nil {
		slog.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	advanced := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2, rejection_reason = $4 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, rejected, debitPending, event.Reason,
		)
		if err != nil || updated.RowsAffected() == 0 {
			return err
		}
		advanced = true
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: debitRejected, Service: bankAName, ObservedAt: event.ObservedAt,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		})
	})
	if err != nil {
		return fmt.Errorf("record debit rejection for transfer %s: %w", event.TransferID, err)
	}
	if !advanced {
		slog.Warn("ignore DebitRejected for transfer not awaiting a debit", "transfer_id", event.TransferID, "message_id", msg.UUID)
	}
	return nil
}

func (s *Service) fundsCredited(msg *message.Message) error {
	var event messaging.FundsCredited
	if err := messaging.Decode(msg, &event); err != nil {
		slog.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	advanced := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, completed, creditPending,
		)
		if err != nil || updated.RowsAffected() == 0 {
			return err
		}
		advanced = true
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: creditCommitted, Service: bankBName, ObservedAt: event.ObservedAt,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: finished, Service: transferServiceName, ObservedAt: time.Now(), CausationID: msg.UUID,
		})
	})
	if err != nil {
		return fmt.Errorf("record credit for transfer %s: %w", event.TransferID, err)
	}
	if !advanced {
		slog.Warn("ignore FundsCredited for transfer not awaiting a credit", "transfer_id", event.TransferID, "message_id", msg.UUID)
	}
	return nil
}

func record(ctx context.Context, tx pgx.Tx, transferID string, entry historyEntry) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO transfer_history (transfer_id, step, service, observed_at, message_id, causation_id, issued_message_id)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''))`,
		transferID, entry.Step, entry.Service, entry.ObservedAt, entry.MessageID, entry.CausationID, entry.IssuedMessageID,
	)
	return err
}

func (s *Service) find(ctx context.Context, id string) (transfer, error) {
	t := transfer{ID: id}
	err := s.db.QueryRow(ctx,
		`SELECT amount, status, COALESCE(rejection_reason, ''), COALESCE(trace_id, ''), requested_at FROM transfers WHERE transfer_id = $1 AND visitor_id = $2`,
		id, visitor.PreparedID,
	).Scan(&t.Amount, &t.Status, &t.RejectionReason, &t.TraceID, &t.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer{}, errTransferNotFound
	}
	if err != nil {
		return transfer{}, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT step, service, observed_at, recorded_at,
		        COALESCE(message_id, ''), COALESCE(causation_id, ''), COALESCE(issued_message_id, '')
		 FROM transfer_history WHERE transfer_id = $1 ORDER BY entry_id`,
		id,
	)
	if err != nil {
		return transfer{}, err
	}
	t.History, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (historyEntry, error) {
		var e historyEntry
		err := row.Scan(&e.Step, &e.Service, &e.ObservedAt, &e.RecordedAt, &e.MessageID, &e.CausationID, &e.IssuedMessageID)
		return e, err
	})
	return t, err
}

func (s *Service) list(ctx context.Context) ([]transfer, error) {
	rows, err := s.db.Query(ctx,
		`SELECT transfer_id, amount, status, requested_at FROM transfers WHERE visitor_id = $1 ORDER BY requested_at DESC`,
		visitor.PreparedID,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (transfer, error) {
		var t transfer
		err := row.Scan(&t.ID, &t.Amount, &t.Status, &t.RequestedAt)
		return t, err
	})
}
