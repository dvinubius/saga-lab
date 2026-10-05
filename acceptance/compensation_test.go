package acceptance_test

import "testing"

func TestCreditRejectionEndsRefunded(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "credit_rejection"}`)
	refunded := demo.awaitReadiness(t, accepted.TransferID, "refunded")

	demo.assertBalances(t, 100, 0)
	assertSteps(t, refunded.History, "requested", "debit_committed", "credit_requested", "credit_rejected", "refund_requested", "refund_committed", "transfer_refunded")
	assertBalancePair(t, entry(t, refunded.History, "debit_committed"), 100, 75)
	assertBalancePair(t, entry(t, refunded.History, "credit_rejected"), 0, 0)
	assertBalancePair(t, entry(t, refunded.History, "refund_committed"), 75, 100)
	assertNoBalancePair(t, refunded.History, "debit_committed", "credit_rejected", "refund_committed")
	if refunded.RejectionReason != "Credit refused by Bank B" {
		t.Errorf("rejection reason = %q, want Bank B's", refunded.RejectionReason)
	}
	assertNoObservations(t, refunded.History)
}

func TestRefundRedeliveryRefundsOnce(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "refund_redelivery"}`)
	refunded := demo.awaitReadiness(t, accepted.TransferID, "refunded")

	demo.assertBalances(t, 100, 0)
	assertSteps(t, refunded.History, "requested", "debit_committed", "credit_requested", "credit_rejected", "refund_requested", "refund_committed", "transfer_refunded")

	history := refunded.History
	requested, refund := entry(t, history, "refund_requested"), entry(t, history, "refund_committed")
	assertBalancePair(t, refund, 75, 100)
	assertNoBalancePair(t, history, "debit_committed", "credit_rejected", "refund_committed")
	nacks, duplicates := observations(history, "NackRequested"), observations(history, "DuplicateSuppressed")
	if len(nacks) != 1 || len(duplicates) != 1 {
		t.Fatalf("observations: %d NackRequested and %d DuplicateSuppressed, want one each; history %+v", len(nacks), len(duplicates), history)
	}
	nack, duplicate := nacks[0], duplicates[0]
	for _, o := range []historyEntry{nack, duplicate} {
		if o.CausationID != requested.IssuedMessageID {
			t.Errorf("%s causation_id = %q, want the RefundFunds message %q", o.Observation, o.CausationID, requested.IssuedMessageID)
		}
	}
	if refund.AttemptID == "" || nack.AttemptID != refund.AttemptID {
		t.Errorf("NackRequested attempt_id = %q, want the refund_committed attempt %q", nack.AttemptID, refund.AttemptID)
	}
	if duplicate.AttemptID == "" || duplicate.AttemptID == refund.AttemptID {
		t.Errorf("DuplicateSuppressed attempt_id = %q, want an attempt other than refund_committed's %q", duplicate.AttemptID, refund.AttemptID)
	}
}

func TestCreditRejectionRejectsAnUnaffordableDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 101, "scenario": "credit_rejection"}`)
	rejected := demo.awaitTransfer(t, accepted.TransferID, "rejected")

	demo.assertBalances(t, 100, 0)
	assertSteps(t, rejected.History, "requested", "debit_rejected", "transfer_rejected")
	if !rejected.VisualisationReady {
		t.Error("visualisation_ready = false, want true")
	}
	assertNoObservations(t, rejected.History)
}

func assertNoObservations(t *testing.T, history []historyEntry) {
	t.Helper()
	for _, e := range history {
		if e.Observation != "" {
			t.Errorf("history has observation %s, want none; history %+v", e.Observation, history)
		}
	}
}
