package acceptance_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSmokeScriptPassesAgainstAFreshStack(t *testing.T) {
	t.Parallel()
	demo := startComposeDemonstration(t)

	out, err := smoke(t, demo.baseURL)
	if err != nil {
		t.Fatalf("smoke: %v\n%s", err, out)
	}
	for _, scenario := range []string{"happy_path", "debit_redelivery", "credit_rejection", "bank_b_unavailable", "refund_redelivery"} {
		if !strings.Contains(out, "ok   "+scenario+"\n") {
			t.Errorf("smoke output lacks a pass for %s:\n%s", scenario, out)
		}
	}
}

func TestSmokeScriptSkipsBankBUnavailableRefusedByTheAdmissionLimit(t *testing.T) {
	t.Parallel()
	demo := startProject(t, []string{"SAGA_LAB_OTLP_ENDPOINT=", "SAGA_LAB_BANK_B_RESUME_WAIT=10m"}, "transfer-service")
	demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	for range 5 {
		if waiting := demo.visitor(t).submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`); waiting.Status != "awaiting_admission" {
			t.Fatalf("waiting = %+v", waiting)
		}
	}

	out, err := smoke(t, demo.baseURL)
	if err != nil {
		t.Fatalf("smoke: %v\n%s", err, out)
	}
	if !strings.Contains(out, "WARN bank_b_unavailable skipped: refused by the admission limit\n") {
		t.Errorf("smoke output lacks the skip warning:\n%s", out)
	}
	for _, scenario := range []string{"happy_path", "debit_redelivery", "credit_rejection", "refund_redelivery"} {
		if !strings.Contains(out, "ok   "+scenario+"\n") {
			t.Errorf("smoke output lacks a pass for %s:\n%s", scenario, out)
		}
	}
}

func smoke(t *testing.T, baseURL string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "../scripts/smoke.sh", baseURL).CombinedOutput()
	t.Logf("smoke output:\n%s", out)
	return string(out), err
}
