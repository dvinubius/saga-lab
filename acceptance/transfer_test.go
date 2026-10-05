package acceptance_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"
)

func TestTransferCompletesAfterBothBanksCommit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25}`)
	completed := demo.awaitTransfer(t, accepted.TransferID, "completed")

	demo.assertBalances(t, 75, 25)
	if completed.Amount != 25 {
		t.Errorf("amount = %d, want 25", completed.Amount)
	}
	if completed.Scenario != "happy_path" {
		t.Errorf("scenario = %q, want happy_path", completed.Scenario)
	}
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_requested", "credit_committed", "finished")
	assertServices(t, completed.History, "Transfer Service", "Bank A", "Transfer Service", "Bank B", "Transfer Service")
	if !completed.VisualisationReady {
		t.Error("visualisation_ready = false, want true")
	}
	for _, entry := range completed.History {
		if entry.Observation != "" {
			t.Errorf("history has observation %s, want none on the happy path", entry.Observation)
		}
	}

	requested, debit, creditRequested, credit, done := completed.History[0], completed.History[1], completed.History[2], completed.History[3], completed.History[4]
	messageIDs := map[string]string{
		"DebitFunds":    requested.IssuedMessageID,
		"FundsDebited":  debit.MessageID,
		"CreditFunds":   creditRequested.IssuedMessageID,
		"FundsCredited": credit.MessageID,
	}
	seen := map[string]string{}
	for message, id := range messageIDs {
		if id == "" {
			t.Errorf("%s message ID missing from history", message)
		} else if other, ok := seen[id]; ok {
			t.Errorf("%s and %s share message ID %q", message, other, id)
		}
		seen[id] = message
	}
	if debit.CausationID != requested.IssuedMessageID {
		t.Errorf("debit_committed causation_id = %q, want DebitFunds %q issued when requested", debit.CausationID, requested.IssuedMessageID)
	}
	if creditRequested.CausationID != debit.MessageID {
		t.Errorf("credit_requested causation_id = %q, want debit outcome %q", creditRequested.CausationID, debit.MessageID)
	}
	if credit.CausationID != creditRequested.IssuedMessageID {
		t.Errorf("credit_committed causation_id = %q, want CreditFunds %q issued when credit requested", credit.CausationID, creditRequested.IssuedMessageID)
	}
	if done.CausationID != credit.MessageID {
		t.Errorf("completed causation_id = %q, want credit outcome %q", done.CausationID, credit.MessageID)
	}
	for _, entry := range completed.History {
		if entry.ObservedAt.IsZero() || entry.RecordedAt.IsZero() {
			t.Errorf("%s observed_at = %v, recorded_at = %v, want both set", entry.Step, entry.ObservedAt, entry.RecordedAt)
		}
	}
}

type transfer struct {
	TransferID      string         `json:"transfer_id"`
	Amount          int64          `json:"amount"`
	Scenario        string         `json:"scenario"`
	Status          string         `json:"status"`
	RejectionReason string         `json:"rejection_reason"`
	TraceID         string         `json:"trace_id"`
	History         []historyEntry `json:"history"`

	VisualisationReady bool `json:"visualisation_ready"`
}

type historyEntry struct {
	Step            string    `json:"step"`
	Observation     string    `json:"observation"`
	Service         string    `json:"service"`
	AttemptID       string    `json:"attempt_id"`
	ObservedAt      time.Time `json:"observed_at"`
	RecordedAt      time.Time `json:"recorded_at"`
	MessageID       string    `json:"message_id"`
	CausationID     string    `json:"causation_id"`
	IssuedMessageID string    `json:"issued_message_id"`
}

func (d *demonstration) submitTransfer(t *testing.T, body string) transfer {
	t.Helper()
	r := d.post(t, "/api/transfers", "application/json", body)
	if r.status != http.StatusAccepted {
		t.Fatalf("POST /api/transfers %s: status %d, body %q", body, r.status, r.body)
	}
	var accepted transfer
	if err := json.Unmarshal(r.body, &accepted); err != nil {
		t.Fatalf("decode accepted transfer %q: %v", r.body, err)
	}
	if accepted.TransferID == "" {
		t.Fatalf("accepted transfer has no ID: %q", r.body)
	}
	if want := "/api/transfers/" + accepted.TransferID; r.location != want {
		t.Errorf("Location = %q, want %q", r.location, want)
	}
	return accepted
}

func (d *demonstration) transfer(t *testing.T, id string) transfer {
	t.Helper()
	body := d.get(t, "/api/transfers/"+id)
	var current transfer
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatalf("decode transfer %q: %v", body, err)
	}
	return current
}

func (d *demonstration) awaitTransfer(t *testing.T, id, status string) transfer {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		current := d.transfer(t, id)
		if current.Status == status {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer %s status = %q after test deadline, want %q", id, current.Status, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func assertSteps(t *testing.T, history []historyEntry, want ...string) {
	t.Helper()
	var got []string
	for _, entry := range history {
		if entry.Step != "" {
			got = append(got, entry.Step)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("history steps = %q, want %q", got, want)
	}
}

func assertServices(t *testing.T, history []historyEntry, want ...string) {
	t.Helper()
	got := make([]string, len(history))
	for i, entry := range history {
		got[i] = entry.Service
	}
	if !slices.Equal(got, want) {
		t.Errorf("history services = %q, want %q", got, want)
	}
}

func TestAnotherTransferRunsAfterCompletion(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	first := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, first.TransferID, "completed")

	second := demo.submitTransfer(t, `{"amount": 30}`)
	if second.TransferID == first.TransferID {
		t.Fatalf("second transfer reused ID %s", first.TransferID)
	}
	completed := demo.awaitTransfer(t, second.TransferID, "completed")

	assertSteps(t, completed.History, "requested", "debit_committed", "credit_requested", "credit_committed", "finished")
	demo.assertBalances(t, 45, 55)
	if transfers := demo.transfers(t); len(transfers) != 2 {
		t.Errorf("transfers = %+v, want 2", transfers)
	}
}
