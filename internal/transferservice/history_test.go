package transferservice

import (
	"reflect"
	"testing"
)

func TestHistoryRowsNameTheCauseAndNumberAttemptsPerCommand(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: transferServiceName, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: bankAName, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds", IssuedMessageID: "credit-funds"},
		{Observation: nackRequested, Service: bankAName, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Observation: duplicateSuppressed, Service: bankAName, AttemptID: "attempt-a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Step: creditCommitted, Service: bankBName, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Step: finished, Service: transferServiceName, CausationID: "funds-credited"},
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
