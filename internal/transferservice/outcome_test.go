package transferservice

import (
	"encoding/json"
	"testing"
)

func balance(n int64) *int64 { return &n }

func TestOutcomeSummarisesBalancesAndCommands(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "a1", MessageID: "funds-debited", CausationID: "debit-funds", BalanceBefore: balance(100), BalanceAfter: balance(75)},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditCommitted, AttemptID: "b1", MessageID: "funds-credited", CausationID: "credit-funds", BalanceBefore: balance(0), BalanceAfter: balance(25)},
		{Step: finished, CausationID: "funds-credited"},
	}

	assertOutcome(t, deriveOutcome(history), `{
		"balances": {"bank_a": {"before": 100, "after": 75}, "bank_b": {"before": 0, "after": 25, "involved": true}},
		"commands": {"debit": {"attempts": 1, "effects": 1}, "credit": {"attempts": 1, "effects": 1}, "refund": null},
		"duplicates_suppressed": 0,
		"duplicate_effects": 0
	}`)
}

func TestOutcomeLeavesMissingBalancePairsNull(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditCommitted, AttemptID: "b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Step: finished, CausationID: "funds-credited"},
	}

	assertOutcome(t, deriveOutcome(history), `{
		"balances": {"bank_a": {"before": null, "after": null}, "bank_b": {"before": null, "after": null, "involved": true}},
		"commands": {"debit": {"attempts": 1, "effects": 1}, "credit": {"attempts": 1, "effects": 1}, "refund": null},
		"duplicates_suppressed": 0,
		"duplicate_effects": 0
	}`)
}

func TestOutcomeMarksBankBNotInvolvedAfterADebitRejection(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitRejected, AttemptID: "a1", MessageID: "debit-rejected", CausationID: "debit-funds", BalanceBefore: balance(100), BalanceAfter: balance(100)},
		{Step: transferRejected, CausationID: "debit-rejected"},
	}

	assertOutcome(t, deriveOutcome(history), `{
		"balances": {"bank_a": {"before": 100, "after": 100}, "bank_b": {"before": null, "after": null, "involved": false}},
		"commands": {"debit": {"attempts": 1, "effects": 0}, "credit": null, "refund": null},
		"duplicates_suppressed": 0,
		"duplicate_effects": 0
	}`)
}

func TestOutcomeCountsARedeliveredDebitAsTwoAttemptsAndOneEffect(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "a1", MessageID: "funds-debited", CausationID: "debit-funds", BalanceBefore: balance(100), BalanceAfter: balance(75)},
		{Observation: nackRequested, AttemptID: "a1", MessageID: "nack", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Observation: duplicateSuppressed, AttemptID: "a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Observation: creditConfirmed, MessageID: "confirmed", CausationID: "credit-funds"},
		{Observation: deliveryPaused, AttemptID: "b1", MessageID: "paused", CausationID: "credit-funds"},
		{Step: creditCommitted, AttemptID: "b1", MessageID: "funds-credited", CausationID: "credit-funds", BalanceBefore: balance(0), BalanceAfter: balance(25)},
		{Step: finished, CausationID: "funds-credited"},
	}

	assertOutcome(t, deriveOutcome(history), `{
		"balances": {"bank_a": {"before": 100, "after": 75}, "bank_b": {"before": 0, "after": 25, "involved": true}},
		"commands": {"debit": {"attempts": 2, "effects": 1}, "credit": {"attempts": 1, "effects": 1}, "refund": null},
		"duplicates_suppressed": 1,
		"duplicate_effects": 0
	}`)
}

func TestOutcomeTakesBankAAfterFromARedeliveredRefund(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "a1", MessageID: "funds-debited", CausationID: "debit-funds", BalanceBefore: balance(100), BalanceAfter: balance(75)},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "b1", MessageID: "credit-rejected", CausationID: "credit-funds", BalanceBefore: balance(0), BalanceAfter: balance(0)},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "a2", MessageID: "funds-refunded", CausationID: "refund-funds", BalanceBefore: balance(75), BalanceAfter: balance(100)},
		{Observation: nackRequested, AttemptID: "a2", MessageID: "nack", CausationID: "refund-funds"},
		{Observation: duplicateSuppressed, AttemptID: "a3", MessageID: "duplicate", CausationID: "refund-funds"},
		{Step: transferRefunded, CausationID: "funds-refunded"},
	}

	assertOutcome(t, deriveOutcome(history), `{
		"balances": {"bank_a": {"before": 100, "after": 100}, "bank_b": {"before": 0, "after": 0, "involved": true}},
		"commands": {"debit": {"attempts": 1, "effects": 1}, "credit": {"attempts": 1, "effects": 0}, "refund": {"attempts": 2, "effects": 1}},
		"duplicates_suppressed": 1,
		"duplicate_effects": 0
	}`)
}

func TestOutcomeCountsASecondCommittedEffectAsADuplicateEffect(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "a1", MessageID: "funds-debited", CausationID: "debit-funds", BalanceBefore: balance(100), BalanceAfter: balance(75)},
		{Step: debitCommitted, AttemptID: "a2", MessageID: "funds-debited-again", CausationID: "debit-funds", BalanceBefore: balance(75), BalanceAfter: balance(50)},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditCommitted, AttemptID: "b1", MessageID: "funds-credited", CausationID: "credit-funds", BalanceBefore: balance(0), BalanceAfter: balance(25)},
		{Step: finished, CausationID: "funds-credited"},
	}

	assertOutcome(t, deriveOutcome(history), `{
		"balances": {"bank_a": {"before": 100, "after": 50}, "bank_b": {"before": 0, "after": 25, "involved": true}},
		"commands": {"debit": {"attempts": 2, "effects": 2}, "credit": {"attempts": 1, "effects": 1}, "refund": null},
		"duplicates_suppressed": 0,
		"duplicate_effects": 1
	}`)
}

func assertOutcome(t *testing.T, got outcome, want string) {
	t.Helper()
	var gotJSON, wantJSON any
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &gotJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatal(err)
	}
	gotText, _ := json.Marshal(gotJSON)
	wantText, _ := json.Marshal(wantJSON)
	if string(gotText) != string(wantText) {
		t.Errorf("outcome = %s\nwant      %s", gotText, wantText)
	}
}
