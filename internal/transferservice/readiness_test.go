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

	const refundCommand = "refund-command"
	refundSteps := []historyEntry{
		requestedStep,
		{Step: debitCommitted, AttemptID: "debit", CausationID: debitCommand, IssuedMessageID: creditCommand},
		{Step: creditRequested, IssuedMessageID: creditCommand},
		{Step: creditRejected, AttemptID: "credit", CausationID: creditCommand},
		{Step: refundRequested, IssuedMessageID: refundCommand},
		{Step: refundCommitted, AttemptID: "first", CausationID: refundCommand},
		{Step: transferRefunded},
	}
	refundNack := historyEntry{Observation: nackRequested, AttemptID: "first", CausationID: refundCommand}
	refundDuplicate := historyEntry{Observation: duplicateSuppressed, AttemptID: "second", CausationID: refundCommand}
	refundedWith := func(extra ...historyEntry) []historyEntry { return append(refundSteps[:7:7], extra...) }

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
		{"Bank B unavailable rejected", bankBUnavailable, rejected, []historyEntry{requestedStep, {Step: debitRejected}}, true},
		{"Bank B unavailable completed missing confirmation", bankBUnavailable, completed, []historyEntry{{Observation: observation("DeliveryResumed")}}, false},
		{"Bank B unavailable completed missing resumption", bankBUnavailable, completed, []historyEntry{{Observation: creditAccepted}}, false},
		{"Bank B unavailable completed without pause", bankBUnavailable, completed, []historyEntry{{Observation: creditAccepted}, {Observation: observation("DeliveryResumed")}}, true},
		{"Bank B unavailable pending with confirmation", bankBUnavailable, creditPending, []historyEntry{requestedStep, debitStep, {Observation: creditAccepted}}, false},
		{"happy path pending", happyPath, creditPending, steps[:2], false},
		{"credit rejection refunded", creditRejection, refunded, []historyEntry{requestedStep, debitStep, {Step: creditRequested}, {Step: creditRejected}, {Step: refundRequested}, {Step: refundCommitted}, {Step: transferRefunded}}, true},
		{"credit rejection pending its refund", creditRejection, refundPending, []historyEntry{requestedStep, debitStep, {Step: creditRequested}, {Step: creditRejected}, {Step: refundRequested}}, false},
		{"debit redelivery pending with all evidence", debitRedelivery, creditPending, []historyEntry{requestedStep, debitStep, nack, duplicate}, false},
		{"evidence arriving after completed", debitRedelivery, completed, append(steps[:4:4], nack, duplicate), true},
		{"out-of-order evidence", debitRedelivery, completed, []historyEntry{requestedStep, duplicate, debitStep, nack, creditStep, finishedStep}, true},
		{"completed before any evidence", debitRedelivery, completed, steps, false},
		{"missing NackRequested", debitRedelivery, completed, append(steps[:4:4], duplicate), false},
		{"missing DuplicateSuppressed", debitRedelivery, completed, append(steps[:4:4], nack), false},
		{"matching attempt IDs", debitRedelivery, completed, append(steps[:4:4], nack, historyEntry{Observation: duplicateSuppressed, AttemptID: "first", CausationID: debitCommand}), false},
		{"NackRequested from another attempt", debitRedelivery, completed, append(steps[:4:4], historyEntry{Observation: nackRequested, AttemptID: "second", CausationID: debitCommand}, duplicate), false},
		{"refund evidence arriving after refunded", refundRedelivery, refunded, refundedWith(refundNack, refundDuplicate), true},
		{"refund redelivery refunded before any evidence", refundRedelivery, refunded, refundedWith(), false},
		{"refund redelivery pending its refund with all evidence", refundRedelivery, refundPending, append(refundSteps[:5:5], refundNack, refundDuplicate), false},
		{"out-of-order refund evidence", refundRedelivery, refunded, []historyEntry{refundSteps[0], refundSteps[1], refundSteps[2], refundSteps[3], refundSteps[4], refundDuplicate, refundSteps[5], refundNack, refundSteps[6]}, true},
		{"missing refund NackRequested", refundRedelivery, refunded, refundedWith(refundDuplicate), false},
		{"matching refund attempt IDs", refundRedelivery, refunded, refundedWith(refundNack, historyEntry{Observation: duplicateSuppressed, AttemptID: "first", CausationID: refundCommand}), false},
		{"refund DuplicateSuppressed caused by DebitFunds", refundRedelivery, refunded, refundedWith(refundNack, historyEntry{Observation: duplicateSuppressed, AttemptID: "debit-again", CausationID: debitCommand}), false},
		{"refund DuplicateSuppressed caused by CreditFunds", refundRedelivery, refunded, refundedWith(refundNack, historyEntry{Observation: duplicateSuppressed, AttemptID: "credit-again", CausationID: creditCommand}), false},
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
