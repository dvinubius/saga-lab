package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestOnlyOnePendingTransferIsAdmitted(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)

	responses := demo.submitConcurrently(t, 8, `{"amount": 25}`)
	var admitted []transfer
	for _, r := range responses {
		if r.status == http.StatusAccepted {
			var accepted transfer
			if err := json.Unmarshal(r.body, &accepted); err != nil {
				t.Fatalf("decode accepted transfer %q: %v", r.body, err)
			}
			admitted = append(admitted, accepted)
		}
	}
	if len(admitted) != 1 {
		t.Fatalf("admitted %d of %d overlapping transfers, want 1: %+v", len(admitted), len(responses), admitted)
	}
	pending := admitted[0].TransferID
	for _, r := range responses {
		if r.status == http.StatusAccepted {
			continue
		}
		if r.status != http.StatusConflict {
			t.Errorf("overlapping submission: status %d, want %d; body %q", r.status, http.StatusConflict, r.body)
			continue
		}
		var problem struct {
			Error             string `json:"error"`
			PendingTransferID string `json:"pending_transfer_id"`
		}
		if err := json.Unmarshal(r.body, &problem); err != nil || problem.Error == "" {
			t.Errorf("body %q does not explain the conflict", r.body)
		}
		if problem.PendingTransferID != pending {
			t.Errorf("conflict names pending transfer %q, want %q", problem.PendingTransferID, pending)
		}
	}

	if transfers := demo.transfers(t); len(transfers) != 1 || transfers[0].Status != "debit_pending" {
		t.Errorf("transfers = %+v, want only %s, pending its debit", transfers, pending)
	}

	release()
	completed := demo.awaitTransfer(t, pending, "completed")
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_requested", "credit_committed", "finished")
	demo.assertBalances(t, 75, 25)

	next := demo.submitTransfer(t, `{"amount": 10}`)
	demo.awaitTransfer(t, next.TransferID, "completed")
	demo.assertBalances(t, 65, 35)
}

func TestPageHoldsSubmissionWhileATransferIsPending(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)

	r := demo.post(t, "/transfers", "application/x-www-form-urlencoded", "amount=25")
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST /transfers: status %d, want %d; body %q", r.status, http.StatusSeeOther, r.body)
	}
	transferPage := r.location
	pending := strings.TrimPrefix(transferPage, "/transfers/")

	home := demo.get(t, "/")
	if !submissionDisabled(home) {
		t.Errorf("home page allows submission while %s is pending", pending)
	}
	if got := pendingTransferLink(home); got != transferPage {
		t.Errorf("home page links pending transfer %q, want %q", got, transferPage)
	}
	if !regexp.MustCompile(`<meta http-equiv="refresh" content="1; url=/">`).Match(home) {
		t.Errorf("home page does not poll while %s is pending", pending)
	}

	r = demo.post(t, "/transfers", "application/x-www-form-urlencoded", "amount=10")
	if r.status != http.StatusConflict {
		t.Fatalf("overlapping POST /transfers: status %d, want %d; Location %q", r.status, http.StatusConflict, r.location)
	}
	if !regexp.MustCompile(`id="overlap-error"`).Match(r.body) {
		t.Errorf("page does not explain the overlapping submission: %s", r.body)
	}
	if regexp.MustCompile(`<meta http-equiv="refresh"`).Match(r.body) {
		t.Errorf("overlap page navigates away from its explanation")
	}
	if got := pendingTransferLink(r.body); got != transferPage {
		t.Errorf("overlap page links pending transfer %q, want %q", got, transferPage)
	}
	if transfers := demo.transfers(t); len(transfers) != 1 {
		t.Errorf("transfers = %+v, want only %s", transfers, pending)
	}

	release()
	demo.awaitPage(t, transferPage, "completed")
	home = demo.get(t, "/")
	if submissionDisabled(home) || pendingTransferLink(home) != "" {
		t.Errorf("home page still holds submission after %s completed", pending)
	}
	if r := demo.post(t, "/transfers", "application/x-www-form-urlencoded", "amount=10"); r.status != http.StatusSeeOther {
		t.Errorf("POST /transfers after completion: status %d, want %d", r.status, http.StatusSeeOther)
	}
}

func submissionDisabled(page []byte) bool {
	return regexp.MustCompile(`<button[^>]*disabled`).Match(page)
}

func pendingTransferLink(page []byte) string {
	match := regexp.MustCompile(`id="pending-transfer"[^>]*href="([^"]*)"`).FindSubmatch(page)
	if match == nil {
		return ""
	}
	return string(match[1])
}

func (d *inProcessDemonstration) holdBankADebits(t *testing.T) (release func()) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, d.bankA.settings.DatabaseURL)
	if err != nil {
		t.Fatalf("connect to Bank A database: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT balance FROM accounts WHERE visitor_id = 'prepared-visitor' FOR UPDATE`); err != nil {
		t.Fatalf("lock prepared account: %v", err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			if err := tx.Rollback(ctx); err != nil {
				t.Errorf("release prepared account: %v", err)
			}
			conn.Close(ctx)
		})
	}
	t.Cleanup(release)
	return release
}

func (d *demonstration) submitConcurrently(t *testing.T, n int, body string) []response {
	t.Helper()
	responses := make([]response, n)
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			responses[i], errs[i] = d.do(http.MethodPost, "/api/transfers", "application/json", body)
		})
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	return responses
}
