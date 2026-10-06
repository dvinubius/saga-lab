package transferservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type status string

const (
	awaitingAdmission status = "awaiting_admission"
	debitPending      status = "debit_pending"
	creditPending     status = "credit_pending"
	refundPending     status = "refund_pending"
	completed         status = "completed"
	rejected          status = "rejected"
	refunded          status = "refunded"
)

var pendingStatuses = []status{awaitingAdmission, debitPending, creditPending, refundPending}

func (s status) Pending() bool {
	return slices.Contains(pendingStatuses, s)
}

func (s status) Label() string {
	switch s {
	case awaitingAdmission:
		return "Another visitor is trying this demo. Yours will start automatically when it's your turn."
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

func (s step) LaneLabel() string {
	label := s.Label()
	for _, bank := range []string{"Bank A ", "Bank B "} {
		if rest, found := strings.CutPrefix(label, bank); found {
			return strings.ToUpper(rest[:1]) + rest[1:]
		}
	}
	return label
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
	admitted            observation = "Admitted"
	nackRequested       observation = messaging.NackRequested
	duplicateSuppressed observation = messaging.DuplicateSuppressed
	creditConfirmed     observation = messaging.CreditConfirmed
	deliveryResumed     observation = messaging.DeliveryResumed
	deliveryPaused      observation = messaging.DeliveryPaused
	deliveryWaiting     observation = "DeliveryWaiting"
)

func (o observation) Label() string {
	switch o {
	case admitted:
		return "admitted"
	case nackRequested:
		return "Couldn’t acknowledge after commit"
	case creditConfirmed:
		return "broker confirmed command"
	case deliveryResumed:
		return "Command delivered"
	case deliveryWaiting:
		return "Command delivery waiting"
	case duplicateSuppressed:
		return "Redelivery rejected; nothing applied"
	}
	return string(o)
}

func (o observation) Title() string {
	switch o {
	case deliveryWaiting:
		return "Service unavailable"
	case deliveryResumed:
		return "Service back up"
	}
	return ""
}

type transferSummary struct {
	ID          string    `json:"transfer_id"`
	Amount      int64     `json:"amount"`
	Scenario    scenario  `json:"scenario"`
	Status      status    `json:"status"`
	RequestedAt time.Time `json:"requested_at"`
}

func (t transfer) StatusLabel() string {
	if t.Status == rejected && t.RejectionReason != "" {
		return fmt.Sprintf("%s (%s)", t.Status.Label(), strings.ToLower(t.RejectionReason))
	}
	return t.Status.Label()
}

type transfer struct {
	transferSummary
	RejectionReason string         `json:"rejection_reason,omitempty"`
	TraceID         string         `json:"trace_id,omitempty"`
	History         []historyEntry `json:"history,omitempty"`

	VisualisationReady bool            `json:"visualisation_ready"`
	OutcomeSummary     *outcomeSummary `json:"outcome,omitempty"`
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
	BalanceBefore   *int64      `json:"balance_before,omitempty"`
	BalanceAfter    *int64      `json:"balance_after,omitempty"`
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
	debit, err := messaging.New(ctx, t.ID, messaging.DebitFunds{TransferID: t.ID, VisitorID: visitor.ID(ctx), Amount: amount, Scenario: string(chosen)}, "")
	if err != nil {
		return transfer{}, err
	}
	for {
		recorded, err := s.recordSubmission(ctx, t, debit)
		if err != nil {
			return transfer{}, fmt.Errorf("record transfer: %w", err)
		}
		if recorded {
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

func (s *Service) recordSubmission(ctx context.Context, t transfer, debit *message.Message) (bool, error) {
	insertedTransfer := false
	queued := false
	traceContext := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, traceContext)
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if t.Scenario == bankBUnavailable {
			holder, err := lockSlot(ctx, tx)
			if err != nil {
				return err
			}
			var waiting bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM transfers WHERE status = $1)`, awaitingAdmission).Scan(&waiting); err != nil {
				return err
			}
			if holder != "" || waiting {
				t.Status = awaitingAdmission
				queued = true
			}
		}
		inserted, err := tx.Exec(ctx,
			`INSERT INTO transfers (transfer_id, visitor_id, amount, scenario, status, requested_at, trace_id, trace_context) VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8)
			 ON CONFLICT (visitor_id) WHERE status IN ('awaiting_admission', 'debit_pending', 'credit_pending', 'refund_pending') DO NOTHING`,
			t.ID, visitor.ID(ctx), t.Amount, t.Scenario, t.Status, t.RequestedAt, t.TraceID, traceContext,
		)
		if err != nil || inserted.RowsAffected() == 0 {
			return err
		}
		insertedTransfer = true
		issuedID := debit.UUID
		if queued {
			issuedID = ""
		}
		if t.Scenario == bankBUnavailable && !queued {
			if _, err := tx.Exec(ctx, `UPDATE demonstration_slot SET holder_transfer_id = $1`, t.ID); err != nil {
				return err
			}
		}
		if err := record(ctx, tx, t.ID, historyEntry{
			Step: requested, Service: messaging.TransferService, ObservedAt: t.RequestedAt, IssuedMessageID: issuedID,
		}); err != nil {
			return err
		}
		if queued {
			return nil
		}
		return messaging.Enqueue(ctx, tx, messaging.DebitFundsTopic, debit)
	})
	if err == nil && insertedTransfer && queued {
		s.logger.Info("transfer queued for admission", "transfer_id", t.ID)
	}
	return insertedTransfer, err
}

func (s *Service) pendingTransferID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRow(ctx,
		`SELECT transfer_id FROM transfers WHERE visitor_id = $1 AND status = ANY($2)`,
		visitor.ID(ctx), pendingStatuses,
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
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), BalanceBefore: event.BalanceBefore, BalanceAfter: event.BalanceAfter,
		}); err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: creditRequested, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID, IssuedMessageID: command.UUID,
		}); err != nil {
			return err
		}
		topic := messaging.CreditFundsTopic
		if credit.Scenario == messaging.BankBUnavailable {
			topic = messaging.CreditFundsDedicatedTopic
		}
		return messaging.Enqueue(ctx, tx, topic, command)
	})
	return endTransition(err, messaging.FundsDebitedTopic, event.TransferID, current, msg, logger)
}

func (s *Service) creditConfirmed(ctx context.Context, tx pgx.Tx, msg *message.Message, confirmedAt time.Time) error {
	var credit messaging.CreditFunds
	if err := messaging.Decode(msg, &credit); err != nil {
		s.logger.Error("discard credit confirmation", "error", err)
		return nil
	}
	var observedAt time.Time
	err := tx.QueryRow(ctx, `UPDATE transfers SET credit_confirmed_at = COALESCE(credit_confirmed_at, $2), resume_at = COALESCE(resume_at, $3) WHERE transfer_id = $1 RETURNING credit_confirmed_at`, credit.TransferID, confirmedAt, confirmedAt.Add(s.resumeWait)).Scan(&observedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		s.logger.Warn("ignore credit confirmation for unknown transfer", "transfer_id", credit.TransferID, "message_id", msg.UUID)
		return nil
	}
	if err != nil {
		return err
	}
	return record(ctx, tx, credit.TransferID, historyEntry{
		Observation: creditConfirmed, Service: messaging.TransferService, ObservedAt: observedAt, MessageID: msg.UUID, CausationID: msg.UUID,
	})
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
		if err := lockSlotFor(ctx, tx, event.TransferID); err != nil {
			return err
		}
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
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), BalanceBefore: event.BalanceBefore, BalanceAfter: event.BalanceAfter,
		}); err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: transferRejected, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID,
		}); err != nil {
			return err
		}
		return s.releaseSlot(ctx, tx, event.TransferID)
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
		if err := lockSlotFor(ctx, tx, event.TransferID); err != nil {
			return err
		}
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
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), BalanceBefore: event.BalanceBefore, BalanceAfter: event.BalanceAfter,
		}); err != nil {
			return err
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Step: finished, Service: messaging.TransferService, ObservedAt: time.Now(), CausationID: msg.UUID,
		}); err != nil {
			return err
		}
		return s.releaseSlot(ctx, tx, event.TransferID)
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
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), BalanceBefore: event.BalanceBefore, BalanceAfter: event.BalanceAfter,
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
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg), BalanceBefore: event.BalanceBefore, BalanceAfter: event.BalanceAfter,
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
		if event.Observation == messaging.DeliveryPaused {
			if _, err := lockSlot(ctx, tx); err != nil {
				return err
			}
		}
		if err := record(ctx, tx, event.TransferID, historyEntry{
			Observation: observation(event.Observation), Service: event.Service, ObservedAt: event.ObservedAt, AttemptID: event.AttemptID,
			MessageID: msg.UUID, CausationID: messaging.CausationID(msg),
		}); err != nil {
			return err
		}
		if event.Observation == messaging.DeliveryPaused {
			return s.releaseSlot(ctx, tx, event.TransferID)
		}
		return nil
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
		`INSERT INTO transfer_history (transfer_id, step, observation, service, observed_at, attempt_id, message_id, causation_id, issued_message_id, balance_before, balance_after)
		 VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4, $5, NULLIF($6, ''), NULLIF($7, ''), NULLIF($8, ''), NULLIF($9, ''), $10, $11)
		 ON CONFLICT (message_id) DO NOTHING`,
		transferID, entry.Step, entry.Observation, entry.Service, entry.ObservedAt, entry.AttemptID, entry.MessageID, entry.CausationID, entry.IssuedMessageID, entry.BalanceBefore, entry.BalanceAfter,
	)
	return err
}

func (s *Service) find(ctx context.Context, id string) (transfer, error) {
	t := transfer{transferSummary: transferSummary{ID: id}}
	err := s.db.QueryRow(ctx,
		`SELECT amount, scenario, status, COALESCE(rejection_reason, ''), COALESCE(trace_id, ''), requested_at FROM transfers WHERE transfer_id = $1 AND visitor_id = $2`,
		id, visitor.ID(ctx),
	).Scan(&t.Amount, &t.Scenario, &t.Status, &t.RejectionReason, &t.TraceID, &t.RequestedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer{}, errTransferNotFound
	}
	if err != nil {
		return transfer{}, err
	}
	rows, err := s.db.Query(ctx,
		`SELECT COALESCE(step, ''), COALESCE(observation, ''), service, COALESCE(attempt_id, ''), observed_at, recorded_at,
		        COALESCE(message_id, ''), COALESCE(causation_id, ''), COALESCE(issued_message_id, ''), balance_before, balance_after
		 FROM transfer_history WHERE transfer_id = $1 ORDER BY observed_at, entry_id`,
		id,
	)
	if err != nil {
		return transfer{}, err
	}
	t.History, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (historyEntry, error) {
		var e historyEntry
		err := row.Scan(&e.Step, &e.Observation, &e.Service, &e.AttemptID, &e.ObservedAt, &e.RecordedAt, &e.MessageID, &e.CausationID, &e.IssuedMessageID, &e.BalanceBefore, &e.BalanceAfter)
		return e, err
	})
	t.VisualisationReady = visualisationReady(t.Scenario, t.Status, t.History)
	if t.VisualisationReady {
		s := summariseOutcome(t.History)
		t.OutcomeSummary = &s
	}
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
	case chosen == bankBUnavailable:
		confirmed, resumed := false, false
		for _, e := range history {
			confirmed = confirmed || e.Observation == creditConfirmed
			resumed = resumed || e.Observation == deliveryResumed
		}
		return confirmed && resumed
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
		visitor.ID(ctx),
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
