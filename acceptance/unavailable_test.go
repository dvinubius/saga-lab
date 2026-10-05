package acceptance_test

import (
	"context"
	"encoding/json"
	"github.com/dvinubius/saga-lab/internal/bank"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestBankBUnavailableCompletesAfterWaitingWithoutAConsumer(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	waiting := demo.awaitCreditConfirmed(t, accepted.TransferID)
	demo.assertBalances(t, 75, 0)
	assertSteps(t, waiting.History, "requested", "debit_committed", "credit_requested")
	if waiting.Status != "credit_pending" || waiting.VisualisationReady {
		t.Fatalf("waiting transfer = %+v", waiting)
	}
	confirmations := observations(waiting.History, "CreditConfirmed")
	if len(confirmations) != 1 {
		t.Fatalf("confirmations = %+v, want one", confirmations)
	}
	confirmation := confirmations[0]
	command := entry(t, waiting.History, "credit_requested")
	if confirmation.Service != "Transfer Service" || confirmation.CausationID != command.IssuedMessageID || confirmation.ObservedAt.IsZero() || confirmation.ObservedAt.Before(command.ObservedAt) {
		t.Fatalf("confirmation = %+v, credit requested = %+v", confirmation, command)
	}
	demo.assertDedicatedQueue(t, 1)
	second := demo.visitor(t)
	normal := second.submitTransfer(t, `{"amount":25}`)
	normalCompleted := second.awaitReadiness(t, normal.TransferID, "completed")
	second.assertBalances(t, 75, 25)
	if after := demo.transfer(t, accepted.TransferID); after.Status != "credit_pending" {
		t.Fatalf("waiting transfer advanced: %+v", after)
	}
	demo.assertBalances(t, 75, 0)
	completed := demo.awaitReadiness(t, accepted.TransferID, "completed")
	demo.assertBalances(t, 75, 25)
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_requested", "credit_committed", "finished")
	assertBalancePair(t, entry(t, completed.History, "debit_committed"), 100, 75)
	assertBalancePair(t, entry(t, completed.History, "credit_committed"), 0, 25)
	assertNoBalancePair(t, completed.History, "debit_committed", "credit_committed")
	resumed := observations(completed.History, "DeliveryResumed")
	if len(resumed) != 1 || resumed[0].Service != "Bank B" || resumed[0].ObservedAt.Sub(confirmation.ObservedAt) < 2500*time.Millisecond {
		t.Fatalf("resumed = %+v, confirmation = %+v", resumed, confirmation)
	}
	if normalFinished := entry(t, normalCompleted.History, "finished"); !normalFinished.ObservedAt.Before(resumed[0].ObservedAt) {
		t.Fatalf("normal transfer finished at %v, after resume at %v", normalFinished.ObservedAt, resumed[0].ObservedAt)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		completed = demo.transfer(t, accepted.TransferID)
		paused := observations(completed.History, "DeliveryPaused")
		if len(paused) == 1 {
			if paused[0].Service != "Bank B" || paused[0].CausationID != command.IssuedMessageID {
				t.Fatalf("paused = %+v", paused)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("DeliveryPaused missing: %+v", completed)
		}
		time.Sleep(25 * time.Millisecond)
	}
	demo.assertDedicatedQueue(t, 0)
}

func (d *visitorClient) awaitCreditConfirmed(t *testing.T, id string) transfer {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		current := d.transfer(t, id)
		if len(observations(current.History, "CreditConfirmed")) != 0 {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("CreditConfirmed missing: %+v", current)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (d *inProcessDemonstration) assertDedicatedQueue(t *testing.T, ready int) {
	t.Helper()
	d.assertQueue(t, "CreditFundsDedicated", ready)
}

func (d *inProcessDemonstration) assertQueue(t *testing.T, topic string, ready int) {
	t.Helper()
	brokerURL, err := url.Parse(d.transferService.settings.AMQPURL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := os.Getenv(managementURLVariable) + "/api/queues/" + url.PathEscape(brokerURL.Path[1:]) + "/" + url.PathEscape(topic) + "?disable_stats=true&enable_queue_totals=true"
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err := http.Get(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		var queue struct {
			Ready   *int `json:"messages_ready"`
			Unacked *int `json:"messages_unacknowledged"`
		}
		err = json.NewDecoder(r.Body).Decode(&queue)
		r.Body.Close()
		if err != nil || r.StatusCode != http.StatusOK {
			t.Fatalf("read dedicated queue: status %d, error %v", r.StatusCode, err)
		}
		if queue.Ready == nil || queue.Unacked == nil {
			t.Fatalf("queue totals omitted a required count: %+v", queue)
		}
		consumersURL := os.Getenv(managementURLVariable) + "/api/consumers/" + url.PathEscape(brokerURL.Path[1:])
		response, err := http.Get(consumersURL)
		if err != nil {
			t.Fatal(err)
		}
		var consumers []struct {
			Queue struct {
				Name string `json:"name"`
			} `json:"queue"`
		}
		err = json.NewDecoder(response.Body).Decode(&consumers)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("read consumers: status %d, error %v", response.StatusCode, err)
		}
		consumerCount := 0
		for _, consumer := range consumers {
			if consumer.Queue.Name == topic {
				consumerCount++
			}
		}
		if consumerCount == 0 && *queue.Ready == ready && *queue.Unacked == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s queue = %+v with %d consumers, want no consumers, %d ready and none unacked", topic, queue, consumerCount, ready)
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
	if confirmations := observations(rejected.History, "CreditConfirmed"); len(confirmations) != 0 {
		t.Fatalf("rejected transfer has credit confirmation: %+v", confirmations)
	}
	demo.assertDedicatedQueue(t, 0)
}

func TestResetClearsTheWaitingDedicatedCredit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	demo.awaitCreditConfirmed(t, accepted.TransferID)
	demo.assertDedicatedQueue(t, 1)
	demo.reset(t)
	demo.assertDedicatedQueue(t, 0)
	demo.assertReset(t, accepted.TransferID)
}

func TestResetPurgesResumeDelivery(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	if !demo.bankB.stop(t) {
		t.FailNow()
	}
	brokerURL, err := url.Parse(demo.transferService.settings.AMQPURL)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/exchanges/" + url.PathEscape(brokerURL.Path[1:]) + "/amq.default/publish"
	if err := manageRabbitMQ(http.MethodPost, path, `{"properties":{},"routing_key":"ResumeDelivery","payload":"{}","payload_encoding":"string"}`); err != nil {
		t.Fatal(err)
	}
	demo.assertQueue(t, "ResumeDelivery", 1)
	if err := bank.Reset(context.Background(), demo.bankB.settings, bankBConfig); err != nil {
		t.Fatal(err)
	}
	demo.assertQueue(t, "ResumeDelivery", 0)
}

func TestBankBResumeScheduleSurvivesTransferServiceRestart(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	waiting := demo.awaitCreditConfirmed(t, accepted.TransferID)
	demo.assertDedicatedQueue(t, 1)
	if !demo.transferService.stop(t) {
		t.FailNow()
	}
	demo.transferService.start(t)
	demo.awaitReady(t, demo.transferService)
	completed := demo.awaitReadiness(t, accepted.TransferID, "completed")
	confirmation := observations(waiting.History, "CreditConfirmed")[0]
	resumed := observations(completed.History, "DeliveryResumed")
	if len(resumed) != 1 || resumed[0].ObservedAt.Sub(confirmation.ObservedAt) < 2500*time.Millisecond {
		t.Fatalf("resume = %+v, confirmation = %+v", resumed, confirmation)
	}
	demo.assertBalances(t, 75, 25)
}
