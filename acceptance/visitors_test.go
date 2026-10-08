package acceptance_test

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestMalformedVisitorCookieStartsANewVisitor(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	for name, token := range map[string]string{
		"short":   "0123456789abcdef",
		"long":    strings.Repeat("ab", 33),
		"not hex": strings.Repeat("zz", 32),
	} {
		t.Run(name, func(t *testing.T) {
			v := newVisitorClient(demo.baseURL)
			v.setToken(token)
			v.assertBalances(t, 100, 0)
			if got := v.token(t); got == token || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
				t.Fatalf("visitor cookie %q, want a new 64-hex token", got)
			}
		})
	}
}

func TestVisitorAccountsAreKeyedByTheTokensHash(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	demo.assertBalances(t, 100, 0)
	token := demo.token(t)
	hash := sha256.Sum256([]byte(token))
	for _, database := range []string{demo.bankA.settings.DatabaseURL, demo.bankB.settings.DatabaseURL} {
		if n := count(t, database, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, hex.EncodeToString(hash[:])); n != 1 {
			t.Errorf("accounts under the token's hash = %d, want 1", n)
		}
		if n := count(t, database, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, token); n != 0 {
			t.Errorf("accounts under the token itself = %d, want 0", n)
		}
	}
}

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
