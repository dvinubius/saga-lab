package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestBankBUnavailableCreditWaitsWithoutAConsumer(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	waiting := demo.awaitCreditAccepted(t, accepted.TransferID)
	demo.assertBalances(t, 75, 0)
	assertSteps(t, waiting.History, "requested", "debit_committed", "credit_requested")
	if waiting.Status != "credit_pending" || waiting.VisualisationReady {
		t.Fatalf("waiting transfer = %+v", waiting)
	}
	confirmations := observations(waiting.History, "CreditAccepted")
	if len(confirmations) != 1 {
		t.Fatalf("confirmations = %+v, want one", confirmations)
	}
	confirmation := confirmations[0]
	if waiting.CreditConfirmedAt == nil || !waiting.CreditConfirmedAt.Equal(confirmation.ObservedAt) {
		t.Fatalf("stored confirmation = %v, observation = %v", waiting.CreditConfirmedAt, confirmation.ObservedAt)
	}
	command := entry(t, waiting.History, "credit_requested")
	if confirmation.Service != "Transfer Service" || confirmation.CausationID != command.IssuedMessageID || confirmation.ObservedAt.IsZero() || confirmation.ObservedAt.Before(command.ObservedAt) {
		t.Fatalf("confirmation = %+v, credit requested = %+v", confirmation, command)
	}
	demo.assertDedicatedQueue(t, 1)
	second := demo.visitor(t)
	normal := second.submitTransfer(t, `{"amount":25}`)
	second.awaitReadiness(t, normal.TransferID, "completed")
	second.assertBalances(t, 75, 25)
	if after := demo.transfer(t, accepted.TransferID); after.Status != "credit_pending" {
		t.Fatalf("waiting transfer advanced: %+v", after)
	}
	demo.assertBalances(t, 75, 0)
	demo.assertDedicatedQueue(t, 1)
}

func (d *visitorClient) awaitCreditAccepted(t *testing.T, id string) transfer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		current := d.transfer(t, id)
		if len(observations(current.History, "CreditAccepted")) != 0 {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("CreditAccepted missing: %+v", current)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (d *inProcessDemonstration) assertDedicatedQueue(t *testing.T, ready int) {
	t.Helper()
	brokerURL, err := url.Parse(d.transferService.settings.AMQPURL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := os.Getenv(managementURLVariable) + "/api/queues/" + url.PathEscape(brokerURL.Path[1:]) + "/CreditFundsDedicated"
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err := http.Get(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		var queue struct {
			Consumers int `json:"consumers"`
			Ready     int `json:"messages_ready"`
			Unacked   int `json:"messages_unacknowledged"`
		}
		err = json.NewDecoder(r.Body).Decode(&queue)
		r.Body.Close()
		if err != nil || r.StatusCode != http.StatusOK {
			t.Fatalf("read dedicated queue: status %d, error %v", r.StatusCode, err)
		}
		if queue.Consumers == 0 && queue.Ready == ready && queue.Unacked == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dedicated queue = %+v, want no consumers, %d ready and none unacked", queue, ready)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestBankBUnavailableRejectsAnUnaffordableDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	accepted := demo.submitTransfer(t, `{"amount":101,"scenario":"bank_b_unavailable"}`)
	rejected := demo.awaitReadiness(t, accepted.TransferID, "rejected")
	demo.assertBalances(t, 100, 0)
	assertSteps(t, rejected.History, "requested", "debit_rejected", "transfer_rejected")
	if confirmations := observations(rejected.History, "CreditAccepted"); len(confirmations) != 0 {
		t.Fatalf("rejected transfer has credit confirmation: %+v", confirmations)
	}
	demo.assertDedicatedQueue(t, 0)
}

func TestResetClearsTheWaitingDedicatedCredit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	demo.awaitCreditAccepted(t, accepted.TransferID)
	demo.assertDedicatedQueue(t, 1)
	demo.reset(t)
	demo.assertDedicatedQueue(t, 0)
	demo.assertReset(t, accepted.TransferID)
}
