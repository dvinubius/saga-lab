package acceptance_test

import "testing"

func TestCreditRejectionEndsRefunded(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "credit_rejection"}`)
	refunded := demo.awaitReadiness(t, accepted.TransferID, "refunded")

	demo.assertBalances(t, 100, 0)
	assertSteps(t, refunded.History, "requested", "debit_committed", "credit_requested", "credit_rejected", "refund_requested", "refund_committed", "transfer_refunded")
	if refunded.RejectionReason != "Credit refused by Bank B" {
		t.Errorf("rejection reason = %q, want Bank B's", refunded.RejectionReason)
	}
	assertNoObservations(t, refunded.History)
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
