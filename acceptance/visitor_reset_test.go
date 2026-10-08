package acceptance_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestVisitorResetStartsFromACleanSlate(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	earlier := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, earlier.TransferID, "completed")
	demo.post(t, "/api/top-ups", "", "")
	old := demo.visitorID(t)

	r := demo.post(t, "/reset", "application/x-www-form-urlencoded", "")
	if r.status != http.StatusSeeOther || r.location != "/" {
		t.Fatalf("POST /reset: status %d, Location %q, want %d to /", r.status, r.location, http.StatusSeeOther)
	}
	if demo.visitorID(t) == old {
		t.Fatal("visitor cookie unchanged after reset")
	}
	if !regexp.MustCompile(`id="no-transfers"`).Match(demo.get(t, "/")) {
		t.Error("home page lists transfers after reset")
	}
	for _, check := range []struct {
		database string
		query    string
		id       string
	}{
		{demo.transferService.settings.DatabaseURL, `SELECT count(*) FROM visitors WHERE visitor_id = $1`, old},
		{demo.transferService.settings.DatabaseURL, `SELECT count(*) FROM transfers WHERE visitor_id = $1`, old},
		{demo.transferService.settings.DatabaseURL, `SELECT count(*) FROM transfer_history WHERE transfer_id = $1`, earlier.TransferID},
		{demo.bankA.settings.DatabaseURL, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, old},
		{demo.bankB.settings.DatabaseURL, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, old},
	} {
		if n := count(t, check.database, check.query, check.id); n != 0 {
			t.Errorf("%s: %d rows remain for the old visitor", check.query, n)
		}
	}

	demo.assertReset(t, earlier.TransferID)
}

func TestVisitorResetIsRefusedWhileATransferIsPending(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	release := demo.holdBankADebits(t)
	pending := demo.submitTransfer(t, `{"amount": 25}`)
	old := demo.visitorID(t)

	if !regexp.MustCompile(`<button[^>]*id="reset"[^>]*disabled`).Match(demo.get(t, "/")) {
		t.Error("home page allows a reset while a transfer is pending")
	}
	r := demo.post(t, "/reset", "application/x-www-form-urlencoded", "")
	if r.status != http.StatusConflict {
		t.Fatalf("POST /reset while %s is pending: status %d, Location %q", pending.TransferID, r.status, r.location)
	}
	if !regexp.MustCompile(`id="reset-error"`).Match(r.body) {
		t.Errorf("page does not explain the refused reset: %s", r.body)
	}
	if regexp.MustCompile(`<meta http-equiv="refresh"|<script src="/static/poll.js"`).Match(r.body) {
		t.Errorf("refused reset page navigates away from its explanation")
	}
	if demo.visitorID(t) != old {
		t.Error("visitor cookie changed by a refused reset")
	}

	release()
	demo.awaitTransfer(t, pending.TransferID, "completed")
	demo.assertBalances(t, 75, 25)
}

func count(t *testing.T, database, query, id string) int {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, database)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	var n int
	if err := conn.QueryRow(ctx, query, id).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}
