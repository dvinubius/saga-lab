package acceptance_test

import (
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/transferservice"
)

func shortExpiry(config *transferservice.Config) {
	config.VisitorExpiry = time.Second
	config.ExpirySweep = 100 * time.Millisecond
}

func TestIdleVisitorExpiresAndStartsOver(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t, shortExpiry)
	earlier := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, earlier.TransferID, "completed")
	id := demo.visitorID(t)

	demo.awaitExpired(t, id)

	demo.assertBalances(t, 100, 0)
	if got := demo.transfers(t); len(got) != 0 {
		t.Fatalf("transfers after expiry = %+v, want none", got)
	}
	if n := count(t, demo.transferService.settings.DatabaseURL, `SELECT count(*) FROM transfer_history WHERE transfer_id = $1`, earlier.TransferID); n != 0 {
		t.Errorf("%d history entries remain for the expired visitor's transfer", n)
	}
}

func TestBusyVisitorsSurviveExpiry(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t, shortExpiry, func(config *transferservice.Config) { config.ResumeWait = time.Minute })
	holderClient := demo.visitor(t)
	holder := holderClient.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	holderClient.awaitCreditConfirmed(t, holder.TransferID)
	waitingClient := demo.visitor(t)
	waiting := waitingClient.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if waiting.Status != "awaiting_admission" {
		t.Fatalf("waiting = %+v", waiting)
	}
	demo.holdBankADebits(t)
	pending := demo.submitTransfer(t, `{"amount": 25}`)
	idle := demo.visitor(t)

	demo.awaitExpired(t, idle.visitorID(t))

	for _, survivor := range []struct {
		client *visitorClient
		id     string
		status string
	}{
		{demo.visitorClient, pending.TransferID, "debit_pending"},
		{holderClient, holder.TransferID, "credit_pending"},
		{waitingClient, waiting.TransferID, "awaiting_admission"},
	} {
		if got := survivor.client.transfer(t, survivor.id); got.Status != survivor.status {
			t.Errorf("transfer %s after expiry sweep = %+v, want %s", survivor.id, got, survivor.status)
		}
	}
	holderClient.assertBalances(t, 75, 0)
}

func (d *inProcessDemonstration) awaitExpired(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		remaining := count(t, d.transferService.settings.DatabaseURL, `SELECT count(*) FROM visitors WHERE visitor_id = $1`, id) +
			count(t, d.bankA.settings.DatabaseURL, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, id) +
			count(t, d.bankB.settings.DatabaseURL, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, id)
		if remaining == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("visitor %s not expired after the test deadline", id)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
