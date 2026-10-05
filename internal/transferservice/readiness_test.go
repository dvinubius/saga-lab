package transferservice

import "testing"

func TestVisualisationReadiness(t *testing.T) {
	const debitCommand, creditCommand = "debit-command", "credit-command"
	requestedStep := historyEntry{Step: requested, IssuedMessageID: debitCommand}
	debitStep := historyEntry{Step: debitCommitted, AttemptID: "first", CausationID: debitCommand, IssuedMessageID: creditCommand}
	creditStep := historyEntry{Step: creditCommitted, AttemptID: "credit", CausationID: creditCommand}
	finishedStep := historyEntry{Step: finished}
	nack := historyEntry{Observation: nackRequested, AttemptID: "first", CausationID: debitCommand}
	duplicate := historyEntry{Observation: duplicateSuppressed, AttemptID: "second", CausationID: debitCommand}
	steps := []historyEntry{requestedStep, debitStep, creditStep, finishedStep}

	tests := []struct {
		name     string
		scenario scenario
		status   status
		history  []historyEntry
		want     bool
	}{
		{"happy path completed", happyPath, completed, steps, true},
		{"happy path rejected", happyPath, rejected, []historyEntry{requestedStep, {Step: debitRejected}}, true},
		{"debit redelivery rejected", debitRedelivery, rejected, []historyEntry{requestedStep, {Step: debitRejected}}, true},
		{"happy path pending", happyPath, creditPending, steps[:2], false},
		{"credit rejection pending its refund", creditRejection, refundPending, []historyEntry{requestedStep, debitStep, {Step: creditRequested}, {Step: creditRejected}, {Step: refundRequested}}, false},
		{"debit redelivery pending with all evidence", debitRedelivery, creditPending, []historyEntry{requestedStep, debitStep, nack, duplicate}, false},
		{"evidence arriving after completed", debitRedelivery, completed, append(steps[:4:4], nack, duplicate), true},
		{"out-of-order evidence", debitRedelivery, completed, []historyEntry{requestedStep, duplicate, debitStep, nack, creditStep, finishedStep}, true},
		{"completed before any evidence", debitRedelivery, completed, steps, false},
		{"missing NackRequested", debitRedelivery, completed, append(steps[:4:4], duplicate), false},
		{"missing DuplicateSuppressed", debitRedelivery, completed, append(steps[:4:4], nack), false},
		{"matching attempt IDs", debitRedelivery, completed, append(steps[:4:4], nack, historyEntry{Observation: duplicateSuppressed, AttemptID: "first", CausationID: debitCommand}), false},
		{"NackRequested from another attempt", debitRedelivery, completed, append(steps[:4:4], historyEntry{Observation: nackRequested, AttemptID: "second", CausationID: debitCommand}, duplicate), false},
		{"DuplicateSuppressed caused by CreditFunds", debitRedelivery, completed, append(steps[:4:4], nack, historyEntry{Observation: duplicateSuppressed, AttemptID: "credit-again", CausationID: creditCommand}), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := visualisationReady(tt.scenario, tt.status, tt.history); got != tt.want {
				t.Errorf("visualisationReady = %v, want %v", got, tt.want)
			}
		})
	}
}
