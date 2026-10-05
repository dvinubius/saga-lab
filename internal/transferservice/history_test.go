package transferservice

import (
	"reflect"
	"testing"
	"time"
)

func TestHistoryRowsNameTheCauseAndNumberAttemptsPerCommand(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds", IssuedMessageID: "credit-funds"},
		{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Observation: duplicateSuppressed, AttemptID: "attempt-a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Step: creditCommitted, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Step: finished, CausationID: "funds-credited"},
	}

	type shown struct {
		Cause   string
		Attempt int
	}
	var got []shown
	for _, row := range historyRows(history) {
		got = append(got, shown{row.Cause, row.Attempt})
	}

	want := []shown{{"", 0}, {"DebitFunds", 1}, {"DebitFunds", 1}, {"DebitFunds", 2}, {"CreditFunds", 1}, {"FundsCredited", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestHistoryRowsAreOrderedByObservationTime(t *testing.T) {
	start := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	history := []historyEntry{
		{Step: requested, ObservedAt: start},
		{Step: creditCommitted, ObservedAt: start.Add(3 * time.Second)},
		{Observation: duplicateSuppressed, ObservedAt: start.Add(2 * time.Second)},
		{Step: debitCommitted, ObservedAt: start.Add(time.Second)},
		{Observation: nackRequested, ObservedAt: start.Add(time.Second)},
	}

	var got []string
	for _, row := range historyRows(history) {
		got = append(got, string(row.Step)+string(row.Observation))
	}

	want := []string{"requested", "debit_committed", "NackRequested", "DuplicateSuppressed", "credit_committed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}
