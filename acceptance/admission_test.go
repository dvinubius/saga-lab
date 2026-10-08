package acceptance_test

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
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
	if len(observations(rejected.History, "CreditConfirmed")) != 0 {
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
	demo.awaitCreditConfirmed(t, holder.TransferID)
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

func TestAdmissionLimitTurnsAwayFurtherUnavailableTransfers(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	holder := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	visitors := make([]*visitorClient, 7)
	responses := make([]response, 7)
	errs := make([]error, 7)
	for i := range visitors {
		visitors[i] = demo.visitor(t)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range visitors {
		group.Go(func() {
			<-start
			responses[i], errs[i] = visitors[i].do(http.MethodPost, "/api/transfers", "application/json", `{"amount":25,"scenario":"bank_b_unavailable"}`)
		})
	}
	close(start)
	group.Wait()
	var waiting []transfer
	var waitingVisitors, refused []*visitorClient
	for i, r := range responses {
		switch {
		case errs[i] != nil:
			t.Fatal(errs[i])
		case r.status == http.StatusAccepted:
			var accepted transfer
			if err := json.Unmarshal(r.body, &accepted); err != nil || accepted.Status != "awaiting_admission" {
				t.Fatalf("accepted = %s, %v", r.body, err)
			}
			waiting = append(waiting, accepted)
			waitingVisitors = append(waitingVisitors, visitors[i])
		case r.status == http.StatusServiceUnavailable:
			var problem struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(r.body, &problem); err != nil || !strings.Contains(problem.Error, "admission limit") || r.retryAfter != "60" {
				t.Fatalf("refusal = %+v, %v", r, err)
			}
			refused = append(refused, visitors[i])
		default:
			t.Fatalf("submission %d: %+v", i, r)
		}
	}
	if len(waiting) != 5 || len(refused) != 2 {
		t.Fatalf("waiting %d, refused %d; want 5 and 2", len(waiting), len(refused))
	}
	turnedAway := refused[0]
	if transfers := turnedAway.transfers(t); len(transfers) != 0 {
		t.Fatalf("refused visitor has transfers %+v", transfers)
	}
	turnedAway.assertBalances(t, 100, 0)

	page := turnedAway.post(t, "/transfers", "application/x-www-form-urlencoded", "amount=30&scenario=bank_b_unavailable")
	if page.status != http.StatusServiceUnavailable || page.retryAfter != "60" {
		t.Fatalf("page refusal = %d, Retry-After %q", page.status, page.retryAfter)
	}
	body := html.UnescapeString(string(page.body))
	for _, want := range []string{
		`id="admission-limit-error">The Bank B unavailable demo is busy right now. Try again in a minute, or pick another scenario.`,
		`value="30"`,
		`value="bank_b_unavailable" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page refusal lacks %q", want)
		}
	}
	if strings.Contains(body, `<fieldset class="scenarios" disabled`) {
		t.Error("page refusal disables the scenarios")
	}
	if transfers := turnedAway.transfers(t); len(transfers) != 0 {
		t.Fatalf("refused visitor has transfers %+v", transfers)
	}

	happy := turnedAway.submitTransfer(t, `{"amount":10}`)
	release()
	turnedAway.awaitReadiness(t, happy.TransferID, "completed")
	turnedAway.assertBalances(t, 90, 10)
	demo.awaitReadiness(t, holder.TransferID, "completed")
	deadline := time.Now().Add(30 * time.Second)
	for waitingCount(t, waitingVisitors, waiting) == 5 {
		if time.Now().After(deadline) {
			t.Fatal("no waiting transfer was admitted")
		}
		time.Sleep(100 * time.Millisecond)
	}
	again := turnedAway.submitTransfer(t, `{"amount":10,"scenario":"bank_b_unavailable"}`)
	if again.Status != "awaiting_admission" {
		t.Fatalf("accepted again = %+v", again)
	}
}

func waitingCount(t *testing.T, visitors []*visitorClient, transfers []transfer) int {
	t.Helper()
	n := 0
	for i, w := range transfers {
		if visitors[i].transfer(t, w.TransferID).Status == "awaiting_admission" {
			n++
		}
	}
	return n
}
