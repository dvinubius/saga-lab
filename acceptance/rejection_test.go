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
	assertSteps(t, rejected.History, "requested", "debit_rejected", "transfer_rejected")
	assertServices(t, rejected.History, "Transfer Service", "Bank A", "Transfer Service")
	requested, rejection, ended := rejected.History[0], rejected.History[1], rejected.History[2]
	assertBalancePair(t, rejection, 100, 100)
	assertNoBalancePair(t, rejected.History, "debit_rejected")
	assertOutcomeSummary(t, rejected, `{
		"balances": {"bank_a": {"before": 100, "after": 100}, "bank_b": {"before": null, "after": null, "involved": false}},
		"commands": {"debit": {"attempts": 1, "effects": 0}, "credit": null, "refund": null},
		"duplicates_suppressed": 0,
		"duplicate_effects": 0
	}`)
	if rejection.MessageID == "" {
		t.Errorf("DebitRejected message ID missing from history")
	}
	if rejection.CausationID != requested.IssuedMessageID {
		t.Errorf("debit_rejected causation_id = %q, want DebitFunds %q issued when requested", rejection.CausationID, requested.IssuedMessageID)
	}
	if rejection.IssuedMessageID != "" {
		t.Errorf("debit rejection issued message %q, want none", rejection.IssuedMessageID)
	}
	if ended.CausationID != rejection.MessageID {
		t.Errorf("transfer_rejected causation_id = %q, want debit rejection %q", ended.CausationID, rejection.MessageID)
	}

	transferPage := "/transfers/" + rejected.TransferID
	page := demo.awaitPage(t, transferPage, "rejected")
	if got := pageData(page, "rejection-reason"); got != rejected.RejectionReason {
		t.Errorf("page reason = %q, want %q", got, rejected.RejectionReason)
	}
	if got, want := pageSteps(page), []string{"requested", "debit_rejected", "transfer_rejected"}; !slices.Equal(got, want) {
		t.Errorf("page history steps = %q, want %q", got, want)
	}
	if got := pageBalanceChange(page, "debit_rejected"); got != "100" {
		t.Errorf("page debit_rejected balance = %q, want the unchanged 100", got)
	}
	for row, want := range map[string][]string{
		"bank-a": {"Bank A", "100", "100"},
		"bank-b": {"Bank B", "Not involved"},
		"debit":  {"Debit", "1", "0"},
		"credit": {"Credit", "—", "—"},
	} {
		if got := pageOutcomeSummary(page, row); !slices.Equal(got, want) {
			t.Errorf("page outcome %s = %q, want %q", row, got, want)
		}
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
