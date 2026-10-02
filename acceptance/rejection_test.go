package acceptance_test

import (
	"reflect"
	"slices"
	"testing"
)

func TestBankARejectsAnUnaffordableDebit(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 101}`)
	rejected := demo.awaitTransfer(t, accepted.TransferID, "rejected")

	demo.assertBalances(t, 100, 0)
	if rejected.RejectionReason == "" {
		t.Errorf("rejected transfer has no reason")
	}
	assertSteps(t, rejected.History, "requested", "debit_rejected")
	assertServices(t, rejected.History, "transfer-service", "bank-a")
	requested, rejection := rejected.History[0], rejected.History[1]
	if rejection.MessageID == "" {
		t.Errorf("DebitRejected message ID missing from history")
	}
	if rejection.CausationID != requested.IssuedMessageID {
		t.Errorf("debit_rejected causation_id = %q, want DebitFunds %q issued when requested", rejection.CausationID, requested.IssuedMessageID)
	}
	if rejection.IssuedMessageID != "" {
		t.Errorf("debit rejection issued message %q, want none", rejection.IssuedMessageID)
	}

	transferPage := "/transfers/" + rejected.TransferID
	page := demo.awaitPage(t, transferPage, "rejected")
	if got := pageData(page, "rejection-reason"); got != rejected.RejectionReason {
		t.Errorf("page reason = %q, want %q", got, rejected.RejectionReason)
	}
	if got, want := pageSteps(page), []string{"requested", "debit_rejected"}; !slices.Equal(got, want) {
		t.Errorf("page history steps = %q, want %q", got, want)
	}
	for range 3 {
		demo.get(t, transferPage)
		demo.get(t, "/api/transfers/"+rejected.TransferID)
	}

	next := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, next.TransferID, "completed")
	demo.assertBalances(t, 75, 25)
	if after := demo.transfer(t, rejected.TransferID); !reflect.DeepEqual(after, rejected) {
		t.Errorf("rejected transfer changed later:\nbefore %+v\nafter  %+v", rejected, after)
	}
	if transfers := demo.transfers(t); len(transfers) != 2 {
		t.Errorf("transfers = %+v, want 2", transfers)
	}
}
