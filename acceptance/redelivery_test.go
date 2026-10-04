package acceptance_test

import "testing"

func TestDebitRedeliveryCompletesWithoutADuplicateDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "debit_redelivery"}`)
	completed := demo.awaitTransfer(t, accepted.TransferID, "completed")

	demo.assertBalances(t, 75, 25)
	if completed.Scenario != "debit_redelivery" {
		t.Errorf("scenario = %q, want debit_redelivery", completed.Scenario)
	}
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_committed", "finished")
}

func TestDebitRedeliveryRejectsAnUnaffordableDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 101, "scenario": "debit_redelivery"}`)
	rejected := demo.awaitTransfer(t, accepted.TransferID, "rejected")

	demo.assertBalances(t, 100, 0)
	assertSteps(t, rejected.History, "requested", "debit_rejected")
}
