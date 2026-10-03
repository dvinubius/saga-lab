package acceptance_test

import "testing"

func TestRestartPreservesBalancesAndTransfers(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	earlier := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, earlier.TransferID, "completed")

	demo.restart(t)

	demo.assertBalances(t, 75, 25)
	if transfers := demo.transfers(t); len(transfers) != 1 || transfers[0].TransferID != earlier.TransferID {
		t.Fatalf("transfers after restart = %+v, want only %s", transfers, earlier.TransferID)
	}
}
