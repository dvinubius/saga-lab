package transferservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/trace"
)

type status string

const (
	debitPending  status = "debit_pending"
	creditPending status = "credit_pending"
	refundPending status = "refund_pending"
	completed     status = "completed"
	rejected      status = "rejected"
	refunded      status = "refunded"
)

var pendingStatuses = []status{debitPending, creditPending, refundPending}

func (s status) Pending() bool {
	return slices.Contains(pendingStatuses, s)
}

func (s status) Label() string {
	switch s {
	case debitPending:
		return "Waiting for Bank A to debit"
	case creditPending:
		return "Waiting for Bank B to credit"
	case refundPending:
		return "Waiting for Bank A to refund"
	case completed:
		return "Completed"
	case rejected:
		return "Rejected by Bank A"
	case refunded:
		return "Refunded after Bank B rejected the credit"
	}
	return string(s)
}

type step string

const (
	requested        step = "requested"
	debitCommitted   step = "debit_committed"
	creditRequested  step = "credit_requested"
	debitRejected    step = "debit_rejected"
	transferRejected step = "transfer_rejected"
	creditCommitted  step = "credit_committed"
	finished         step = "finished"
	creditRejected   step = "credit_rejected"
	refundRequested  step = "refund_requested"
	refundCommitted  step = "refund_committed"
	transferRefunded step = "transfer_refunded"
)

func (s step) Label() string {
	switch s {
	case requested:
		return "Transfer requested"
	case debitCommitted:
		return "Bank A committed the debit"
	case creditRequested:
		return "Debit confirmed"
	case debitRejected:
		return "Bank A rejected the debit"
	case transferRejected:
		return "Transfer rejected"
	case creditCommitted:
		return "Bank B committed the credit"
	case finished:
		return "Transfer completed"
	case creditRejected:
		return "Bank B rejected the credit"
	case refundRequested:
		return "Credit rejection confirmed"
	case refundCommitted:
		return "Bank A committed the refund"
	case transferRefunded:
		return "Transfer refunded"
	}
	return string(s)
}

func (s step) LabelTail() string {
	switch s {
	case creditRequested:
		return "; credit requested"
	case refundRequested:
		return "; refund requested"
	}
	return ""
}

type observation string

const (
	nackRequested       observation = messaging.NackRequested
	duplicateSuppressed observation = messaging.DuplicateSuppressed
)

func (o observation) Label() string {
	switch o {
	case nackRequested:
		return "couldn’t acknowledge after commit"
	case duplicateSuppressed:
		return "repeat recognised; nothing applied"
	}
	return string(o)
}

type transferSummary struct {
	ID          string    `json:"transfer_id"`
	Amount      int64     `json:"amount"`
	Scenario    scenario  `json:"scenario"`
	Status      status    `json:"status"`
	RequestedAt time.Time `json:"requested_at"`
}

type transfer struct {
	transferSummary
	RejectionReason string         `json:"rejection_reason,omitempty"`
	TraceID         string         `json:"trace_id,omitempty"`
	History         []historyEntry `json:"history,omitempty"`

	VisualisationReady bool `json:"visualisation_ready"`
}

type historyEntry struct {
	Step            step        `json:"step,omitempty"`
	Observation     observation `json:"observation,omitempty"`
	Service         string      `json:"service"`
	AttemptID       string      `json:"attempt_id,omitempty"`
	ObservedAt      time.Time   `json:"observed_at"`
	RecordedAt      time.Time   `json:"recorded_at"`
	MessageID       string      `json:"message_id,omitempty"`
	CausationID     string      `json:"causation_id,omitempty"`
	IssuedMessageID string      `json:"issued_message_id,omitempty"`
}

const foreignKeyViolation = "23503"

var errTransferNotFound = errors.New("transfer not found")

type pendingTransferError struct {
	PendingID string
}

func (e pendingTransferError) Error() string {
	return "transfer " + e.PendingID + " is still pending"
}

func (s *Service) submit(ctx context.Context, amount int64, chosen scenario) (transfer, error) {
	t := transfer{transferSummary: transferSummary{ID: watermill.NewUUID(), Amount: amount, Scenario: chosen, Status: debitPending, RequestedAt: time.Now()}}
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		t.TraceID = span.TraceID().String()
	}
	debit, err := messaging.New(ctx, t.ID, messaging.DebitFunds{TransferID: t.ID, VisitorID: visitor.PreparedID, Amount: amount, Scenario: string(chosen)}, "")
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
			 ON CONFLICT (visitor_id) WHERE status IN ('debit_pending', 'credit_pending', 'refund_pending') DO NOTHING`,
			t.ID, visitor.PreparedID, t.Amount, t.Scenario, t.Status, t.RequestedAt, t.TraceID,
		)
		if err != nil || inserted.RowsAffected() == 0 {
			return err
		}
		admitted = true
		if err := record(ctx, tx, t.ID, historyEntry{
			Step: requested, Service: messaging.TransferService, ObservedAt: t.RequestedAt, IssuedMessageID: debit.UUID,
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
		`SELECT transfer_id FROM transfers WHERE visitor_id = $1 AND status = ANY($2)`,
		visitor.PreparedID, pendingStatuses,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Service) fundsDebited(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), s.logger)
	var event messaging.FundsDebited
	if err := messaging.Decode(msg, &event); err != nil {
		logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	current := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		credit := messaging.CreditFunds{TransferID: event.TransferID}
		err := tx.QueryRow(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3 RETURNING visitor_id, amount, scenario`,
			event.TransferID, creditPending, debitPending,
		).Scan(&credit.VisitorID, &credit.Amount, &credit.Scenario)
		if errors.Is(err, pgx.ErrNoRows) {
			current, err = currentStatus(ctx, tx, event.TransferID)
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
			Step: debitCommitted, Service: messaging.BankA, ObservedAt: event.ObservedAt, AttemptID: messaging.ProducerAttemptID(msg),
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: creditRequested, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID, IssuedMessageID: command.UUID,
		}); err != nil {
			return err
		}
		return messaging.Enqueue(ctx, tx, messaging.CreditFundsTopic, command)
	})
	return endTransition(err, messaging.FundsDebitedTopic, event.TransferID, current, msg, logger)
}

func (s *Service) debitRejected(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), s.logger)
	var event messaging.DebitRejected
	if err := messaging.Decode(msg, &event); err != nil {
		logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	current := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2, rejection_reason = $4 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, rejected, debitPending, event.Reason,
		)
		if err != nil {
			return err
		}
		if updated.RowsAffected() == 0 {
			current, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: debitRejected, Service: messaging.BankA, ObservedAt: event.ObservedAt, AttemptID: messaging.ProducerAttemptID(msg),
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: transferRejected, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID,
		})
	})
	return endTransition(err, messaging.DebitRejectedTopic, event.TransferID, current, msg, logger)
}

func (s *Service) fundsCredited(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), s.logger)
	var event messaging.FundsCredited
	if err := messaging.Decode(msg, &event); err != nil {
		logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	current := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, completed, creditPending,
		)
		if err != nil {
			return err
		}
		if updated.RowsAffected() == 0 {
			current, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: creditCommitted, Service: messaging.BankB, ObservedAt: event.ObservedAt, AttemptID: messaging.ProducerAttemptID(msg),
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: finished, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID,
		})
	})
	return endTransition(err, messaging.FundsCreditedTopic, event.TransferID, current, msg, logger)
}

func (s *Service) creditRejected(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), s.logger)
	var event messaging.CreditRejected
	if err := messaging.Decode(msg, &event); err != nil {
		logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	current := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		refund := messaging.RefundFunds{TransferID: event.TransferID}
		err := tx.QueryRow(ctx,
			`UPDATE transfers SET status = $2, rejection_reason = $4 WHERE transfer_id = $1 AND status = $3 RETURNING visitor_id, amount, scenario`,
			event.TransferID, refundPending, creditPending, event.Reason,
		).Scan(&refund.VisitorID, &refund.Amount, &refund.Scenario)
		if errors.Is(err, pgx.ErrNoRows) {
			current, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
		if err != nil {
			return err
		}
		command, err := messaging.New(ctx, event.TransferID, refund, msg.UUID)
		if err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: creditRejected, Service: messaging.BankB, ObservedAt: event.ObservedAt, AttemptID: messaging.ProducerAttemptID(msg),
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: refundRequested, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID, IssuedMessageID: command.UUID,
		}); err != nil {
			return err
		}
		return messaging.Enqueue(ctx, tx, messaging.RefundFundsTopic, command)
	})
	return endTransition(err, messaging.CreditRejectedTopic, event.TransferID, current, msg, logger)
}

func (s *Service) fundsRefunded(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), s.logger)
	var event messaging.FundsRefunded
	if err := messaging.Decode(msg, &event); err != nil {
		logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	current := ""
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		updated, err := tx.Exec(ctx,
			`UPDATE transfers SET status = $2 WHERE transfer_id = $1 AND status = $3`,
			event.TransferID, refunded, refundPending,
		)
		if err != nil {
			return err
		}
		if updated.RowsAffected() == 0 {
			current, err = currentStatus(ctx, tx, event.TransferID)
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: refundCommitted, Service: messaging.BankA, ObservedAt: event.ObservedAt, AttemptID: messaging.ProducerAttemptID(msg),
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		return record(ctx, tx, event.TransferID, historyEntry{
			Step: transferRefunded, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID,
		})
	})
	return endTransition(err, messaging.FundsRefundedTopic, event.TransferID, current, msg, logger)
}

func (s *Service) processingObserved(msg *message.Message) error {
	logger := messaging.AttemptLogger(msg.Context(), s.logger)
	var event messaging.ProcessingObserved
	if err := messaging.Decode(msg, &event); err != nil {
		logger.Error("discard event", "error", err)
		return nil
	}
	ctx := msg.Context()
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return record(ctx, tx, event.TransferID, historyEntry{
			Observation: observation(event.Observation), Service: event.Service, ObservedAt: event.ObservedAt, AttemptID: event.AttemptID,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		})
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == foreignKeyViolation {
		logger.Info("ignore observation for unknown transfer", "observation", event.Observation, "transfer_id", event.TransferID, "message_id", msg.UUID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("record %s for transfer %s: %w", event.Observation, event.TransferID, err)
	}
	return nil
}

func endTransition(err error, event, transferID, guardMissStatus string, msg *message.Message, logger *slog.Logger) error {
	if err != nil {
		return fmt.Errorf("record %s for transfer %s: %w", event, transferID, err)
	}
	if guardMissStatus != "" {
		logger.Info("ignore event that fails the status guard", "event", event, "transfer_id", transferID, "message_id", msg.UUID, "status", guardMissStatus)
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
		`INSERT INTO transfer_history (transfer_id, step, observation, service, observed_at, attempt_id, message_id, causation_id, issued_message_id)
		 VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5, NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''))
		 ON CONFLICT (message_id) DO NOTHING`,
		transferID, entry.Step, entry.Observation, entry.Service, entry.ObservedAt, entry.AttemptID, entry.MessageID, entry.CausationID, entry.IssuedMessageID,
	)
	return err
}

func (s *Service) find(ctx context.Context, id string) (transfer, error) {
	t := transfer{transferSummary: transferSummary{ID: id}}
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
		`SELECT COALESCE(step, ''), COALESCE(observation, ''), service, COALESCE(attempt_id, ''), observed_at, recorded_at,
		        COALESCE(message_id, ''), COALESCE(causation_id, ''), COALESCE(issued_message_id, '')
		 FROM transfer_history WHERE transfer_id = $1 ORDER BY observed_at, entry_id`,
		id,
	)
	if err != nil {
		return transfer{}, err
	}
	t.History, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (historyEntry, error) {
		var e historyEntry
		err := row.Scan(&e.Step, &e.Observation, &e.Service, &e.AttemptID, &e.ObservedAt, &e.RecordedAt, &e.MessageID, &e.CausationID, &e.IssuedMessageID)
		return e, err
	})
	t.VisualisationReady = visualisationReady(t.Scenario, t.Status, t.History)
	return t, err
}

func visualisationReady(chosen scenario, current status, history []historyEntry) bool {
	switch {
	case current == refunded && chosen == refundRedelivery:
		return redeliveryEvidenced(history, messaging.RefundFundsTopic, refundCommitted)
	case current == rejected, current == refunded:
		return true
	case current != completed:
		return false
	case chosen == debitRedelivery:
		return redeliveryEvidenced(history, messaging.DebitFundsTopic, debitCommitted)
	}
	return true
}

func redeliveryEvidenced(history []historyEntry, command string, committedBy step) bool {
	topics := messageTopics(history)
	committingAttempt := ""
	for _, e := range history {
		if e.Step == committedBy {
			committingAttempt = e.AttemptID
		}
	}
	nacked, suppressed := false, false
	for _, e := range history {
		if topics[e.CausationID] != command || e.AttemptID == "" {
			continue
		}
		switch e.Observation {
		case nackRequested:
			nacked = nacked || e.AttemptID == committingAttempt
		case duplicateSuppressed:
			suppressed = suppressed || e.AttemptID != committingAttempt
		}
	}
	return nacked && suppressed
}

func (s *Service) list(ctx context.Context) ([]transferSummary, error) {
	rows, err := s.db.Query(ctx,
		`SELECT transfer_id, amount, scenario, status, requested_at FROM transfers WHERE visitor_id = $1 ORDER BY requested_at DESC`,
		visitor.PreparedID,
	)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (transferSummary, error) {
		var t transferSummary
		err := row.Scan(&t.ID, &t.Amount, &t.Scenario, &t.Status, &t.RequestedAt)
		return t, err
	})
}
