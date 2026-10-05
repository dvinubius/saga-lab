package acceptance_test

import (
	"net/http"
	"testing"
)

func TestVisitorsHaveTheirOwnAccountsAndTransfers(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	first := demo.visitor(t)
	second := demo.visitor(t)
	first.assertBalances(t, 100, 0)
	second.assertBalances(t, 100, 0)
	submitted := first.submitTransfer(t, `{"amount":25}`)
	first.awaitTransfer(t, submitted.TransferID, "completed")
	first.assertBalances(t, 75, 25)
	second.assertBalances(t, 100, 0)
	if got := first.transfers(t); len(got) != 1 || got[0].TransferID != submitted.TransferID {
		t.Fatalf("first visitor transfers = %+v", got)
	}
	if got := second.transfers(t); len(got) != 0 {
		t.Fatalf("second visitor transfers = %+v, want none", got)
	}
	for _, path := range []string{"/api/transfers/", "/transfers/"} {
		r := second.request(t, http.MethodGet, path+submitted.TransferID, "", "")
		if r.status != http.StatusNotFound {
			t.Errorf("another visitor's %s: status %d, want 404", path, r.status)
		}
	}
}

func TestOneVisitorsPendingTransferDoesNotBlockAnother(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	pending := demo.submitTransfer(t, `{"amount":25}`)
	other := demo.visitor(t)
	submitted := other.submitTransfer(t, `{"amount":10}`)
	if got := demo.transfer(t, pending.TransferID); got.Status != "debit_pending" {
		t.Fatalf("first transfer = %+v, want pending debit", got)
	}
	release()
	other.awaitTransfer(t, submitted.TransferID, "completed")
	other.assertBalances(t, 90, 10)
	demo.awaitTransfer(t, pending.TransferID, "completed")
	demo.assertBalances(t, 75, 25)
}
