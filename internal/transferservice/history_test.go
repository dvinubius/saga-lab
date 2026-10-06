package transferservice

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

func TestHistoryRowsNameTheCauseAndNumberAttemptsPerCommand(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Observation: duplicateSuppressed, AttemptID: "attempt-a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Step: creditCommitted, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Step: finished, CausationID: "funds-credited"},
	}

	type shown struct {
		Cause   string
		Attempt int
	}
	var got []shown
	for _, row := range historyRows(history) {
		got = append(got, shown{row.Cause, row.Attempt})
	}

	want := []shown{{"", 0}, {"DebitFunds", 1}, {"DebitFunds", 1}, {"FundsDebited", 0}, {"DebitFunds", 2}, {"CreditFunds", 1}, {"FundsCredited", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}
}

func TestHistoryRowsShowTheCausingMessageAndAttemptIDs(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
	}

	var got []string
	for _, row := range historyRows(history) {
		got = append(got, row.Tooltip)
	}

	want := []string{"", "message debit-funds\nattempt attempt-a1", "message funds-debited"}
	if !slices.Equal(got, want) {
		t.Fatalf("tooltips = %q, want %q", got, want)
	}
}

func TestBankACommittedTheRefundAlwaysExplainsTheRefund(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
	}

	rows := historyRows(history)

	if got, want := []string{rows[3].Cause, rows[4].Cause}, []string{"CreditFunds", "CreditRejected"}; !slices.Equal(got, want) {
		t.Errorf("causes = %q, want %q", got, want)
	}
	if rows[3].Attempt != 1 {
		t.Errorf("credit_rejected attempt = %d, want 1", rows[3].Attempt)
	}
	if rows[4].About != "" {
		t.Error("refund_requested has a note, want the refund explained on refund_committed")
	}
	if rows[5].About == "" {
		t.Error("refund_committed has no note, want one explaining the refund")
	}
}

func TestRefundRowsNameTheirMessagesAndNumberRefundAttempts(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
		{Observation: duplicateSuppressed, AttemptID: "attempt-a3", MessageID: "duplicate", CausationID: "refund-funds"},
		{Step: transferRefunded, CausationID: "funds-refunded"},
	}

	type shown struct {
		Cause   string
		Attempt int
	}
	var got []shown
	for _, row := range historyRows(history)[5:] {
		got = append(got, shown{row.Cause, row.Attempt})
	}

	want := []shown{{"RefundFunds", 1}, {"RefundFunds", 2}, {"FundsRefunded", 0}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refund rows = %v, want %v", got, want)
	}
}

func TestTransferRefundedIsExplainedOnlyAfterALostRefundAcknowledgement(t *testing.T) {
	refunded := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
		{Step: transferRefunded, CausationID: "funds-refunded"},
	}
	debitAckLost := append(slices.Clone(refunded), historyEntry{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"})
	refundAckLost := append(slices.Clone(refunded), historyEntry{Observation: nackRequested, AttemptID: "attempt-a2", MessageID: "nack", CausationID: "refund-funds"})

	for name, tc := range map[string]struct {
		history []historyEntry
		want    bool
	}{
		"credit rejection":            {refunded, false},
		"lost debit acknowledgement":  {debitAckLost, false},
		"lost refund acknowledgement": {refundAckLost, true},
	} {
		rows := historyRows(tc.history)
		if explained := rows[6].About != ""; explained != tc.want {
			t.Errorf("%s: transfer_refunded explained = %v, want %v", name, explained, tc.want)
		}
		if name == "lost refund acknowledgement" && rows[2].About != "" {
			t.Errorf("%s: credit_requested explained, want the debit note only after a lost debit acknowledgement", name)
		}
	}
}

func TestCreditRequestedIsExplainedOnlyAfterALostDebitAcknowledgement(t *testing.T) {
	happy := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
	}
	redelivered := append(slices.Clone(happy), historyEntry{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"})

	for name, history := range map[string][]historyEntry{"happy path": happy, "lost acknowledgement": redelivered} {
		explained := historyRows(history)[2].About != ""
		if want := name == "lost acknowledgement"; explained != want {
			t.Errorf("%s: credit_requested explained = %v, want %v", name, explained, want)
		}
	}
}

func TestARowContinuesTheRowAboveOnlyForTheSameAttempt(t *testing.T) {
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Observation: nackRequested, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Observation: duplicateSuppressed, AttemptID: "attempt-a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
	}

	var got []bool
	for _, row := range historyRows(history) {
		got = append(got, row.Continues)
	}

	want := []bool{false, false, true, false, false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("continues = %v, want %v", got, want)
	}
}

func TestUnavailableBankBWaitsAfterTheCreditConfirmation(t *testing.T) {
	confirmation := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	history := []historyEntry{
		{Step: creditRequested, IssuedMessageID: "credit-funds"},
		{Observation: creditConfirmed, ObservedAt: confirmation, CausationID: "credit-funds"},
		{Observation: deliveryResumed, Service: "Bank B", ObservedAt: confirmation.Add(2500 * time.Millisecond)},
		{Observation: deliveryPaused, Service: "Bank B"},
	}
	rows := historyRows(history)
	if rows[1].Cause != "→ [Sent CreditFunds]" || !rows[1].AboutCause || rows[1].About != "The broker has the message for Bank B." || rows[1].Label != "broker confirmed command" {
		t.Fatalf("confirmation row = %+v", rows[1])
	}
	waiting := rows[2]
	if waiting.Lane != 2 || waiting.Observation != deliveryWaiting || waiting.Label != "Command delivery waiting" || waiting.Observation.Title() != "Service unavailable" || !waiting.ObservedAt.IsZero() {
		t.Fatalf("waiting row = %+v", waiting)
	}
	if waiting.About != "A missing consumer simulates Bank B being down. The credit command waits in the broker's queue with no consumer, neither delivered nor failed." {
		t.Fatalf("waiting note = %q", waiting.About)
	}
	if rows[3].Label != "Command delivered after 2.5s" || rows[3].Observation.Title() != "Service back up" || rows[3].About != "" {
		t.Fatalf("resume row = %+v", rows[3])
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %+v, want delivery paused left out", rows[4:])
	}
}

func TestAdmissionHistoryShowsWaitAndLinksTheDebit(t *testing.T) {
	requestedAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, ObservedAt: requestedAt},
		{Observation: observation("Admitted"), Service: messaging.TransferService, ObservedAt: requestedAt.Add(3250 * time.Millisecond), IssuedMessageID: "admission-debit"},
		{Step: debitCommitted, Service: messaging.BankA, CausationID: "admission-debit"},
	}
	rows := historyRows(history)
	if rows[1].Label != "admitted after waiting 3.2 s for another visitor’s demo" || rows[1].Lane != 0 || rows[2].Cause != "DebitFunds" || rows[0].Cause != "" || rows[1].Cause != "" {
		t.Fatalf("admission/debit rows = %+v / %+v", rows[1], rows[2])
	}
}

func TestHistoryRowsShowEachBankOutcomesBalanceChange(t *testing.T) {
	balance := func(n int64) *int64 { return &n }
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, MessageID: "funds-debited", CausationID: "debit-funds", BalanceBefore: balance(100), BalanceAfter: balance(75)},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, MessageID: "credit-rejected", CausationID: "credit-funds", BalanceBefore: balance(0), BalanceAfter: balance(0)},
		{Step: refundRequested, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, MessageID: "funds-refunded", CausationID: "refund-funds"},
	}

	var got []string
	for _, row := range historyRows(history) {
		got = append(got, row.Balance)
	}

	want := []string{"", "100 → 75", "", "0", "", ""}
	if !slices.Equal(got, want) {
		t.Fatalf("balances = %q, want %q", got, want)
	}
}

func TestPlaybackDwellsForTheRealGapToTheNextRow(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	at := func(ms int) time.Time { return start.Add(time.Duration(ms) * time.Millisecond) }
	history := []historyEntry{
		{Step: requested, IssuedMessageID: "debit-funds", ObservedAt: at(0)},
		{Step: debitCommitted, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds", ObservedAt: at(3)},
		{Step: creditRequested, CausationID: "funds-debited", IssuedMessageID: "credit-funds", ObservedAt: at(1200)},
		{Observation: creditConfirmed, CausationID: "credit-funds", ObservedAt: at(1205)},
		{Observation: deliveryResumed, Service: messaging.BankB, ObservedAt: at(6185)},
		{Step: creditCommitted, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds", ObservedAt: at(6170)},
		{Observation: deliveryPaused, Service: messaging.BankB, ObservedAt: at(6180)},
		{Step: finished, CausationID: "funds-credited", ObservedAt: at(6190)},
	}

	type shown struct {
		PlayedAt time.Time
		Dwell    time.Duration
		Gap      string
	}
	var got []shown
	for _, row := range playback(historyRows(history)) {
		got = append(got, shown{row.PlayedAt, row.Dwell, row.Gap})
	}

	ms := time.Millisecond
	want := []shown{
		{at(0), 700 * ms, "+0.003 s"},
		{at(3), 1197 * ms, "+1.197 s"},
		{at(1200), 700 * ms, "+0.005 s"},
		{at(1205), 700 * ms, "+0.000 s"},
		{at(1205), 4000 * ms, "+4.980 s"},
		{at(6185), 700 * ms, "-0.015 s"},
		{at(6170), 700 * ms, "+0.020 s"},
		{at(6190), 700 * ms, ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("playback =\n%v\nwant\n%v", got, want)
	}
}

func TestPlaybackCapsALongAdmissionWaitAndKeepsItsRealGap(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, ObservedAt: start},
		{Observation: admitted, Service: messaging.TransferService, ObservedAt: start.Add(2500 * time.Millisecond), IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: messaging.BankA, CausationID: "debit-funds", ObservedAt: start.Add(12500 * time.Millisecond)},
	}

	rows := playback(historyRows(history))

	if got, want := []time.Duration{rows[0].Dwell, rows[1].Dwell, rows[2].Dwell}, []time.Duration{2500 * time.Millisecond, 4 * time.Second, 700 * time.Millisecond}; !slices.Equal(got, want) {
		t.Errorf("dwells = %v, want %v", got, want)
	}
	if rows[1].Gap != "+10.000 s" {
		t.Errorf("gap = %q, want the real gap", rows[1].Gap)
	}
}

func litPaths(history []historyEntry) []string {
	var got []string
	for _, row := range historyRows(history) {
		got = append(got, row.Path)
	}
	return got
}

func TestHappyPathLightsEachStepFromSenderThroughTheBrokerToReceiver(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, Service: messaging.TransferService, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditCommitted, Service: messaging.BankB, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Step: finished, Service: messaging.TransferService, CausationID: "funds-credited"},
	}

	want := []string{
		"transfer-service broker",
		"transfer-service broker bank-a",
		"bank-a broker transfer-service",
		"transfer-service broker bank-b",
		"bank-b broker transfer-service",
	}
	if got := litPaths(history); !slices.Equal(got, want) {
		t.Fatalf("paths =\n%q\nwant\n%q", got, want)
	}
}

func TestARedeliveredDebitIsHandedOverAgainByTheBroker(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Observation: nackRequested, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "nack", CausationID: "debit-funds"},
		{Step: creditRequested, Service: messaging.TransferService, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Observation: duplicateSuppressed, Service: messaging.BankA, AttemptID: "attempt-a2", MessageID: "duplicate", CausationID: "debit-funds"},
		{Step: creditCommitted, Service: messaging.BankB, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Step: finished, Service: messaging.TransferService, CausationID: "funds-credited"},
	}

	want := []string{
		"transfer-service broker",
		"transfer-service broker bank-a",
		"bank-a broker",
		"bank-a broker transfer-service",
		"broker bank-a",
		"transfer-service broker bank-b",
		"bank-b broker transfer-service",
	}
	if got := litPaths(history); !slices.Equal(got, want) {
		t.Fatalf("paths =\n%q\nwant\n%q", got, want)
	}
}

func TestTheCreditWaitsInTheBrokerWhileBankBHasNoConsumer(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, Service: messaging.TransferService, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Observation: creditConfirmed, Service: messaging.TransferService, MessageID: "credit-funds", CausationID: "credit-funds"},
		{Observation: deliveryResumed, Service: messaging.BankB},
		{Step: creditCommitted, Service: messaging.BankB, AttemptID: "attempt-b1", MessageID: "funds-credited", CausationID: "credit-funds"},
		{Observation: deliveryPaused, Service: messaging.BankB},
		{Step: finished, Service: messaging.TransferService, CausationID: "funds-credited"},
	}

	type lit struct {
		Path       string
		NoConsumer bool
	}
	var got []lit
	for _, row := range historyRows(history) {
		got = append(got, lit{row.Path, row.NoConsumer})
	}

	want := []lit{
		{"transfer-service broker", false},
		{"transfer-service broker bank-a", false},
		{"bank-a broker transfer-service", false},
		{"transfer-service broker", false},
		{"broker", true},
		{"broker bank-b", false},
		{"transfer-service broker bank-b", false},
		{"bank-b broker transfer-service", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths =\n%v\nwant\n%v", got, want)
	}
}

func TestAnAdmittedTransferPublishesItsDebitToTheBroker(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService},
		{Observation: admitted, Service: messaging.TransferService, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
	}

	want := []string{"transfer-service broker", "transfer-service broker", "transfer-service broker bank-a"}
	if got := litPaths(history); !slices.Equal(got, want) {
		t.Fatalf("paths =\n%q\nwant\n%q", got, want)
	}
}

func TestARejectedDebitTravelsToBankAAndBack(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, IssuedMessageID: "debit-funds"},
		{Step: debitRejected, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "debit-rejected", CausationID: "debit-funds"},
		{Step: transferRejected, Service: messaging.TransferService, CausationID: "debit-rejected"},
	}

	want := []string{"transfer-service broker", "transfer-service broker bank-a", "bank-a broker transfer-service"}
	if got := litPaths(history); !slices.Equal(got, want) {
		t.Fatalf("paths =\n%q\nwant\n%q", got, want)
	}
}

func TestACompensatedTransferWithARedeliveredRefundLightsEveryLeg(t *testing.T) {
	history := []historyEntry{
		{Step: requested, Service: messaging.TransferService, IssuedMessageID: "debit-funds"},
		{Step: debitCommitted, Service: messaging.BankA, AttemptID: "attempt-a1", MessageID: "funds-debited", CausationID: "debit-funds"},
		{Step: creditRequested, Service: messaging.TransferService, CausationID: "funds-debited", IssuedMessageID: "credit-funds"},
		{Step: creditRejected, Service: messaging.BankB, AttemptID: "attempt-b1", MessageID: "credit-rejected", CausationID: "credit-funds"},
		{Step: refundRequested, Service: messaging.TransferService, CausationID: "credit-rejected", IssuedMessageID: "refund-funds"},
		{Step: refundCommitted, Service: messaging.BankA, AttemptID: "attempt-a2", MessageID: "funds-refunded", CausationID: "refund-funds"},
		{Observation: nackRequested, Service: messaging.BankA, AttemptID: "attempt-a2", MessageID: "nack", CausationID: "refund-funds"},
		{Step: transferRefunded, Service: messaging.TransferService, CausationID: "funds-refunded"},
		{Observation: duplicateSuppressed, Service: messaging.BankA, AttemptID: "attempt-a3", MessageID: "duplicate", CausationID: "refund-funds"},
	}

	want := []string{
		"transfer-service broker",
		"transfer-service broker bank-a",
		"bank-a broker transfer-service",
		"transfer-service broker bank-b",
		"bank-b broker transfer-service",
		"transfer-service broker bank-a",
		"bank-a broker",
		"bank-a broker transfer-service",
		"broker bank-a",
	}
	if got := litPaths(history); !slices.Equal(got, want) {
		t.Fatalf("paths =\n%q\nwant\n%q", got, want)
	}
}
