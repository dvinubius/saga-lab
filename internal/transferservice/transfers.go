package transferservice

import (
	"context"
	"errors"
	"fmt"
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
	Scenario        scenario       `json:"scenario"`
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

func (s *Service) submit(ctx context.Context, amount int64, sc scenario) (transfer, error) {
	t := transfer{ID: watermill.NewUUID(), Amount: amount, Scenario: sc, Status: debitPending, RequestedAt: time.Now()}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		t.TraceID = span.TraceID().String()
	}
	debit, err := messaging.New(ctx, t.ID, messaging.DebitFunds{
		AccountOperation: messaging.AccountOperation{TransferID: t.ID, VisitorID: visitor.PreparedID, Amount: amount},
		Scenario:         string(sc),
	}, "")
	if err != nil {
		return transfer{}, err
	}
	for {
		admitted, err := s.admit(ctx, t, debit)
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
	return s.find(ctx, t.ID)
}

func (s *Service) admit(ctx context.Context, t transfer, debit *message.Message) (bool, error) {
	admitted := false
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		inserted, err := tx.Exec(ctx,
			`INSERT INTO transfers (transfer_id, visitor_id, amount, scenario, status, requested_at, trace_id) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
			 ON CONFLICT (visitor_id) WHERE status IN ('debit_pending', 'credit_pending') DO NOTHING`,
			t.ID, visitor.PreparedID, t.Amount, t.Scenario, t.Status, t.RequestedAt, t.TraceID,
		)
		if err != nil || inserted.RowsAffected() == 0 {
			return err
		}
		admitted = true
		if err := record(ctx, tx, t.ID, historyEntry{
			Step: requested, Service: transferServiceName, ObservedAt: t.RequestedAt, IssuedMessageID: debit.UUID,
		}); err != nil {
			return err
		}
		return messaging.Enqueue(ctx, tx, messaging.DebitFundsTopic, debit)
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

func (s *Service) fundsDebited(msg *message.Message) error {
	var event messaging.FundsDebited
	if err := messaging.Decode(msg, &event); err != nil {
		s.logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	missed := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		credit := messaging.CreditFunds{TransferID: event.TransferID}
		err := tx.QueryRow(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3 RETURNING visitor_id, amount`,
			event.TransferID, creditPending, debitPending,
		).Scan(&credit.VisitorID, &credit.Amount)
		if errors.Is(err, pgx.ErrNoRows) {
			missed, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
		if err != nil {
			return err
		}
		command, err := messaging.New(ctx, event.TransferID, credit, msg.UUID)
		if err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: debitCommitted, Service: bankAName, ObservedAt: event.ObservedAt,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), IssuedMessageID: command.UUID,
		}); err != nil {
			return err
		}
		return messaging.Enqueue(ctx, tx, messaging.CreditFundsTopic, command)
	})
	if err != nil {
		return fmt.Errorf("record debit for transfer %s: %w", event.TransferID, err)
	}
	if missed != "" {
		s.logger.Info("ignore event that fails the status guard", "event", "FundsDebited", "transfer_id", event.TransferID, "message_id", msg.UUID, "status", missed)
	}
	return nil
}

func (s *Service) debitRejected(msg *message.Message) error {
	var event messaging.DebitRejected
	if err := messaging.Decode(msg, &event); err != nil {
		s.logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	missed := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2, rejection_reason = $4 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, rejected, debitPending, event.Reason,
		)
		if err != nil {
			return err
		}
		if updated.RowsAffected() == 0 {
			missed, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: debitRejected, Service: bankAName, ObservedAt: event.ObservedAt,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		})
	})
	if err != nil {
		return fmt.Errorf("record debit rejection for transfer %s: %w", event.TransferID, err)
	}
	if missed != "" {
		s.logger.Info("ignore event that fails the status guard", "event", "DebitRejected", "transfer_id", event.TransferID, "message_id", msg.UUID, "status", missed)
	}
	return nil
}

func (s *Service) fundsCredited(msg *message.Message) error {
	var event messaging.FundsCredited
	if err := messaging.Decode(msg, &event); err != nil {
		s.logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	missed := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, completed, creditPending,
		)
		if err != nil {
			return err
		}
		if updated.RowsAffected() == 0 {
			missed, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
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
	if missed != "" {
		s.logger.Info("ignore event that fails the status guard", "event", "FundsCredited", "transfer_id", event.TransferID, "message_id", msg.UUID, "status", missed)
	}
	return nil
}

func currentStatus(ctx context.Context, tx pgx.Tx, transferID string) (string, error) {
	var current string
	err := tx.QueryRow(ctx, `SELECT status FROM transfers WHERE transfer_id = $1`, transferID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return "not found", nil
	}
	return current, err
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
		`SELECT amount, scenario, status, COALESCE(rejection_reason, ''), COALESCE(trace_id, ''), requested_at FROM transfers WHERE transfer_id = $1 AND visitor_id = $2`,
		id, visitor.PreparedID,
	).Scan(&t.Amount, &t.Scenario, &t.Status, &t.RejectionReason, &t.TraceID, &t.RequestedAt)
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
		`SELECT transfer_id, amount, scenario, status, requested_at FROM transfers WHERE visitor_id = $1 ORDER BY requested_at DESC`,
		visitor.PreparedID,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (transfer, error) {
		var t transfer
		err := row.Scan(&t.ID, &t.Amount, &t.Scenario, &t.Status, &t.RequestedAt)
		return t, err
	})
}
