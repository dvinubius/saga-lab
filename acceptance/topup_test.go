package acceptance_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"testing"
)

func TestVisitorTopsUpBankA(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	t.Run("API", func(t *testing.T) {
		visitor := demo.visitor(t)
		r := visitor.post(t, "/api/top-ups", "", "")
		if r.status != http.StatusOK {
			t.Fatalf("POST /api/top-ups: status %d, body %q", r.status, r.body)
		}
		var balances struct {
			BankA struct {
				Balance *int64 `json:"balance"`
			} `json:"bank_a"`
			BankB struct {
				Balance *int64 `json:"balance"`
			} `json:"bank_b"`
		}
		if err := json.Unmarshal(r.body, &balances); err != nil {
			t.Fatalf("decode top-up response %q: %v", r.body, err)
		}
		assertBalance(t, "Bank A", balances.BankA.Balance, 200)
		assertBalance(t, "Bank B", balances.BankB.Balance, 0)
		visitor.assertBalances(t, 200, 0)
	})
	t.Run("page", func(t *testing.T) {
		visitor := demo.visitor(t)
		if topUpDisabled(visitor.get(t, "/")) {
			t.Fatal("home page disables the top-up with no transfer pending")
		}
		r := visitor.post(t, "/top-ups", "application/x-www-form-urlencoded", "")
		if r.status != http.StatusSeeOther || r.location != "/" {
			t.Fatalf("POST /top-ups: status %d, Location %q, want %d to /", r.status, r.location, http.StatusSeeOther)
		}
		page := visitor.get(t, "/")
		assertBalance(t, "Bank A", pageBalance(t, page, "bank-a-balance"), 200)
		assertBalance(t, "Bank B", pageBalance(t, page, "bank-b-balance"), 0)
		if !regexp.MustCompile(`id="no-transfers"`).Match(page) {
			t.Errorf("transfer history after a top-up is not empty: %s", page)
		}
	})
}

func TestTopUpIsRefusedWhileATransferIsPending(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	pending := demo.submitTransfer(t, `{"amount":25}`)
	demo.assertTopUpRefused(t, pending.TransferID)
	release()
}

func TestTopUpIsRefusedWhileATransferAwaitsAdmission(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	demo.submitTransfer(t, `{"amount":101,"scenario":"bank_b_unavailable"}`)
	second := demo.visitor(t)
	waiting := second.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if waiting.Status != "awaiting_admission" {
		t.Fatalf("waiting = %+v", waiting)
	}
	second.assertTopUpRefused(t, waiting.TransferID)
	release()
}

func (d *visitorClient) assertTopUpRefused(t *testing.T, pending string) {
	t.Helper()
	r := d.post(t, "/api/top-ups", "", "")
	var problem struct {
		Error             string `json:"error"`
		PendingTransferID string `json:"pending_transfer_id"`
	}
	if err := json.Unmarshal(r.body, &problem); err != nil || r.status != http.StatusConflict || problem.Error == "" || problem.PendingTransferID != pending {
		t.Errorf("POST /api/top-ups while %s is pending: status %d, body %q", pending, r.status, r.body)
	}

	if !topUpDisabled(d.get(t, "/")) {
		t.Errorf("home page allows a top-up while %s is pending", pending)
	}

	r = d.post(t, "/top-ups", "application/x-www-form-urlencoded", "")
	if r.status != http.StatusConflict {
		t.Fatalf("POST /top-ups while %s is pending: status %d, Location %q", pending, r.status, r.location)
	}
	if !regexp.MustCompile(`id="top-up-error"`).Match(r.body) {
		t.Errorf("page does not explain the refused top-up: %s", r.body)
	}
	if regexp.MustCompile(`<meta http-equiv="refresh"`).Match(r.body) {
		t.Errorf("refused top-up page navigates away from its explanation")
	}
	assertBalance(t, "Bank A", pageBalance(t, r.body, "bank-a-balance"), 100)
	d.assertBalances(t, 100, 0)
}

func topUpDisabled(page []byte) bool {
	return regexp.MustCompile(`<button[^>]*id="top-up"[^>]*disabled`).Match(page)
}
