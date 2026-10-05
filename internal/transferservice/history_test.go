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

func TestBankACommittedTheRefundAlwaysExplainsTheRefund(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
	}

	rows := historyRows(history)

	if got, want := []string{rows[3].Cause, rows[4].Cause}, []string{"CreditFunds", "CreditRejected"}; !slices.Equal(got, want) {
		t.Errorf("causes = %q, want %q", got, want)
	}
	if rows[3].Attempt != 1 {
		t.Errorf("credit_rejected attempt = %d, want 1", rows[3].Attempt)
	}
	if rows[4].About != "" {
		t.Error("refund_requested has a note, want the refund explained on refund_committed")
	}
	if rows[5].About == "" {
		t.Error("refund_committed has no note, want one explaining the refund")
	}
}

func TestRefundRowsNameTheirMessagesAndNumberRefundAttempts(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
		{Observation: duplicateSuppressed, AttemptID: "attempt-a3", MessageID: "duplicate", CausationID: "refund-funds"},
		{Step: transferRefunded, CausationID: "funds-refunded"},
	}

	type shown struct {
		Cause   string
		Attempt int
	}
	var got []shown
	for _, row := range historyRows(history)[5:] {
		got = append(got, shown{row.Cause, row.Attempt})
	}

	want := []shown{{"RefundFunds", 1}, {"RefundFunds", 2}, {"FundsRefunded", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refund rows = %v, want %v", got, want)
	}
}

func TestTransferRefundedIsExplainedOnlyAfterALostRefundAcknowledgement(t *testing.T) {
	refunded := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
		{Step: transferRefunded, CausationID: "funds-refunded"},
	}
	debitAckLost := append(slices.Clone(refunded), historyEntry{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"})
	refundAckLost := append(slices.Clone(refunded), historyEntry{Observation: nackRequested, AttemptID: "attempt-a2", MessageID: "nack", CausationID: "refund-funds"})

	for name, tc := range map[string]struct {
		history []historyEntry
		want    bool
	}{
		"credit rejection":            {refunded, false},
		"lost debit acknowledgement":  {debitAckLost, false},
		"lost refund acknowledgement": {refundAckLost, true},
	} {
		rows := historyRows(tc.history)
		if explained := rows[6].About != ""; explained != tc.want {
			t.Errorf("%s: transfer_refunded explained = %v, want %v", name, explained, tc.want)
		}
		if name == "lost refund acknowledgement" && rows[2].About != "" {
			t.Errorf("%s: credit_requested explained, want the debit note only after a lost debit acknowledgement", name)
		}
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

func TestARowContinuesTheRowAboveOnlyForTheSameAttempt(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Observation: duplicateSuppressed, AttemptID: "attempt-a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
	}

	var got []bool
	for _, row := range historyRows(history) {
		got = append(got, row.Continues)
	}

	want := []bool{false, false, true, false, false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("continues = %v, want %v", got, want)
	}
}
