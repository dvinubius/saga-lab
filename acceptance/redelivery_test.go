package acceptance_test

import (
	"testing"
	"time"
)

func TestDebitRedeliveryCompletesWithoutADuplicateDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "debit_redelivery"}`)
	completed := demo.awaitReadiness(t, accepted.TransferID, "completed")

	demo.assertBalances(t, 75, 25)
	if completed.Scenario != "debit_redelivery" {
		t.Errorf("scenario = %q, want debit_redelivery", completed.Scenario)
	}
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_committed", "finished")

	history := completed.History
	requested, debit := entry(t, history, "requested"), entry(t, history, "debit_committed")
	nacks, duplicates := observations(history, "NackRequested"), observations(history, "DuplicateSuppressed")
	if len(nacks) != 1 || len(duplicates) != 1 {
		t.Fatalf("observations: %d NackRequested and %d DuplicateSuppressed, want one each; history %+v", len(nacks), len(duplicates), history)
	}
	nack, duplicate := nacks[0], duplicates[0]
	for _, o := range []historyEntry{nack, duplicate} {
		if o.CausationID != requested.IssuedMessageID {
			t.Errorf("%s causation_id = %q, want the DebitFunds message %q", o.Observation, o.CausationID, requested.IssuedMessageID)
		}
	}
	if debit.AttemptID == "" || nack.AttemptID != debit.AttemptID {
		t.Errorf("NackRequested attempt_id = %q, want the debit_committed attempt %q", nack.AttemptID, debit.AttemptID)
	}
	if duplicate.AttemptID == "" || duplicate.AttemptID == debit.AttemptID {
		t.Errorf("DuplicateSuppressed attempt_id = %q, want an attempt other than debit_committed's %q", duplicate.AttemptID, debit.AttemptID)
	}
}

func TestDebitRedeliveryRejectsAnUnaffordableDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 101, "scenario": "debit_redelivery"}`)
	rejected := demo.awaitTransfer(t, accepted.TransferID, "rejected")

	demo.assertBalances(t, 100, 0)
	assertSteps(t, rejected.History, "requested", "debit_rejected")
}

func (d *demonstration) awaitReadiness(t *testing.T, id, status string) transfer {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		current := d.transfer(t, id)
		if current.Status == status && current.VisualisationReady {
			return current
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer %s status = %q, visualisation_ready = %v after test deadline, want %q and ready; history %+v", id, current.Status, current.VisualisationReady, status, current.History)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func entry(t *testing.T, history []historyEntry, step string) historyEntry {
	t.Helper()
	for _, e := range history {
		if e.Step == step {
			return e
		}
	}
	t.Fatalf("history has no %s step: %+v", step, history)
	return historyEntry{}
}

func observations(history []historyEntry, observation string) []historyEntry {
	var found []historyEntry
	for _, e := range history {
		if e.Observation == observation {
			found = append(found, e)
		}
	}
	return found
}
