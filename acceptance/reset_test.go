package acceptance_test

import (
	"context"
	"net/http"
	"os/exec"
	"testing"
)

func TestResetRestoresThePreparedDemonstration(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	earlier := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, earlier.TransferID, "completed")

	demo.compose(t, "stop", "transfer-service", "bank-a", "bank-b")
	demo.compose(t, "up", "--detach", "--no-build", "--wait", "transfer-service")
	demo.reconnect(t)
	demo.assertBalances(t, 75, 25)
	if transfers := demo.transfers(t); len(transfers) != 1 || transfers[0].TransferID != earlier.TransferID {
		t.Fatalf("transfers after restart = %+v, want only %s", transfers, earlier.TransferID)
	}

	demo.compose(t, "stop", "bank-a")
	stale := demo.submitTransfer(t, `{"amount": 10}`)

	demo.reset(t)

	demo.assertBalances(t, 100, 0)
	if transfers := demo.transfers(t); len(transfers) != 0 {
		t.Errorf("transfers after reset = %+v, want none", transfers)
	}
	for _, id := range []string{earlier.TransferID, stale.TransferID} {
		if r := demo.request(t, http.MethodGet, "/api/transfers/"+id, "", ""); r.status != http.StatusNotFound {
			t.Errorf("GET transfer %s after reset: status %d, want %d", id, r.status, http.StatusNotFound)
		}
	}
	if home := demo.get(t, "/"); submissionDisabled(home) || pendingTransferLink(home) != "" {
		t.Errorf("home page holds submission after reset")
	}

	again := demo.submitTransfer(t, `{"amount": 25}`)
	completed := demo.awaitTransfer(t, again.TransferID, "completed")
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_committed", "finished")
	demo.assertBalances(t, 75, 25)
	if transfers := demo.transfers(t); len(transfers) != 1 || transfers[0].TransferID != again.TransferID {
		t.Errorf("transfers = %+v, want only %s", transfers, again.TransferID)
	}
}

func (d *demonstration) reset(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), composeDeadline)
	defer cancel()
	script := exec.CommandContext(ctx, "../scripts/reset.sh")
	script.Env = append(composeEnv(d.env), "COMPOSE_PROJECT_NAME="+d.project)
	if out, err := script.CombinedOutput(); err != nil {
		t.Fatalf("reset: %v\n%s", err, out)
	}
	d.reconnect(t)
}
