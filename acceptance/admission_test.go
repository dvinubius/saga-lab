package acceptance_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
)

func TestUnavailableTransfersFromTwoVisitorsRunInTurn(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	first, second := demo.visitorClient, demo.visitor(t)
	first.assertBalances(t, 100, 0)
	second.assertBalances(t, 100, 0)
	visitors := []*visitorClient{first, second}
	responses := make([]response, 2)
	errs := make([]error, 2)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range 2 {
		group.Go(func() {
			<-start
			responses[i], errs[i] = visitors[i].do(http.MethodPost, "/api/transfers", "application/json", `{"amount":25,"scenario":"bank_b_unavailable"}`)
		})
	}
	close(start)
	group.Wait()
	var holder, waiting transfer
	var holderClient, waitingClient *visitorClient
	for i, r := range responses {
		if errs[i] != nil || r.status != http.StatusAccepted {
			t.Fatalf("submission %d: %+v %v", i, r, errs[i])
		}
		var accepted transfer
		if err := json.Unmarshal(r.body, &accepted); err != nil {
			t.Fatal(err)
		}
		if accepted.Status == "awaiting_admission" {
			waiting, waitingClient = accepted, visitors[i]
		} else {
			holder, holderClient = accepted, visitors[i]
		}
	}
	if holderClient == nil || waitingClient == nil {
		t.Fatalf("holder %+v, waiting %+v", holder, waiting)
	}
	waitingClient.assertBalances(t, 100, 0)
	assertSteps(t, waiting.History, "requested")
	if waiting.VisualisationReady || waiting.History[0].IssuedMessageID != "" {
		t.Fatalf("waiting transfer = %+v", waiting)
	}
	conflict := waitingClient.post(t, "/api/transfers", "application/json", `{"amount":10}`)
	var problem struct {
		ID string `json:"pending_transfer_id"`
	}
	if err := json.Unmarshal(conflict.body, &problem); err != nil || conflict.status != http.StatusConflict || problem.ID != waiting.TransferID {
		t.Fatalf("waiting conflict = %+v, %v", conflict, err)
	}
	completedHolder := holderClient.awaitReadiness(t, holder.TransferID, "completed")
	completedWaiting := waitingClient.awaitReadiness(t, waiting.TransferID, "completed")
	holderClient.assertBalances(t, 75, 25)
	waitingClient.assertBalances(t, 75, 25)
	admissions := observations(completedWaiting.History, "Admitted")
	if len(admissions) != 1 || admissions[0].Service != "Transfer Service" || admissions[0].IssuedMessageID == "" || !admissions[0].ObservedAt.After(entry(t, completedHolder.History, "finished").ObservedAt) {
		t.Fatalf("admission = %+v, holder = %+v", admissions, completedHolder)
	}
	if len(observations(completedHolder.History, "Admitted")) != 0 {
		t.Fatalf("uncontended transfer has admission: %+v", completedHolder)
	}
	debit := entry(t, completedWaiting.History, "debit_committed")
	if debit.CausationID != admissions[0].IssuedMessageID {
		t.Fatalf("debit %+v not linked to admission %+v", debit, admissions[0])
	}
}

func TestRejectedUnavailableTransferAdmitsTheWaitingVisitor(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	holder := demo.submitTransfer(t, `{"amount":101,"scenario":"bank_b_unavailable"}`)
	second := demo.visitor(t)
	waiting := second.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if waiting.Status != "awaiting_admission" {
		t.Fatalf("waiting = %+v", waiting)
	}
	second.assertBalances(t, 100, 0)
	release()
	rejected := demo.awaitReadiness(t, holder.TransferID, "rejected")
	if len(observations(rejected.History, "CreditAccepted")) != 0 {
		t.Fatalf("rejected transfer sent credit: %+v", rejected)
	}
	completed := second.awaitReadiness(t, waiting.TransferID, "completed")
	if len(observations(completed.History, "Admitted")) != 1 {
		t.Fatalf("admission missing: %+v", completed)
	}
	demo.assertBalances(t, 100, 0)
	second.assertBalances(t, 75, 25)
}

func TestResetClearsTheDemonstrationSlotAndAdmissionWaits(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	holder := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	demo.awaitCreditAccepted(t, holder.TransferID)
	second := demo.visitor(t)
	waiting := second.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if waiting.Status != "awaiting_admission" {
		t.Fatalf("waiting = %+v", waiting)
	}
	demo.reset(t)
	demo.assertReset(t, holder.TransferID)
	if r, err := second.do(http.MethodGet, "/api/transfers/"+waiting.TransferID, "", ""); err != nil || r.status != http.StatusNotFound {
		t.Fatalf("waiting transfer survived reset: %+v", r)
	}
	next := second.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if next.Status == "awaiting_admission" {
		t.Fatalf("reset left slot occupied: %+v", next)
	}
	second.awaitReadiness(t, next.TransferID, "completed")
}

func TestUncontendedUnavailableTransferStartsWithoutAdmission(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if accepted.Status != "debit_pending" {
		t.Fatalf("uncontended transfer = %+v", accepted)
	}
	release()
	completed := demo.awaitReadiness(t, accepted.TransferID, "completed")
	if len(observations(completed.History, "Admitted")) != 0 {
		t.Fatalf("uncontended transfer admitted: %+v", completed)
	}
	demo.assertBalances(t, 75, 25)
}
