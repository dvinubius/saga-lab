package transferservice

import (
	"reflect"
	"slices"
	"testing"
)

func TestHistoryRowsNameTheCauseAndNumberAttemptsPerCommand(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
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

	want := []shown{{"", 0}, {"DebitFunds", 1}, {"DebitFunds", 1}, {"FundsDebited", 0}, {"DebitFunds", 2}, {"CreditFunds", 1}, {"FundsCredited", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestCreditRequestedIsExplainedOnlyAfterALostDebitAcknowledgement(t *testing.T) {
	happy := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
	}
	redelivered := append(slices.Clone(happy), historyEntry{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"})

	for name, history := range map[string][]historyEntry{"happy path": happy, "lost acknowledgement": redelivered} {
		explained := historyRows(history)[2].About != ""
		if want := name == "lost acknowledgement"; explained != want {
			t.Errorf("%s: credit_requested explained = %v, want %v", name, explained, want)
		}
	}
}
