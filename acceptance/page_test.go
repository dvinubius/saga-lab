package acceptance_test

import (
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"testing"
	"time"
)

func TestRefreshingTheTransferPageNeverResubmits(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	r := demo.post(t, "/transfers", "application/x-www-form-urlencoded", "amount=25")
	if r.status != http.StatusSeeOther {
		t.Fatalf("POST /transfers: status %d, want %d; body %q", r.status, http.StatusSeeOther, r.body)
	}
	match := regexp.MustCompile(`^/transfers/([^/]+)$`).FindStringSubmatch(r.location)
	if match == nil {
		t.Fatalf("redirected to %q, want a transfer page", r.location)
	}
	transferPage, id := r.location, match[1]

	page := demo.awaitPage(t, transferPage, "completed")
	if got := pageData(page, "transfer-id"); got != id {
		t.Errorf("page transfer ID = %q, want %q", got, id)
	}
	assertBalance(t, "Bank A page", pageBalance(t, page, "bank-a-balance"), 75)
	assertBalance(t, "Bank B page", pageBalance(t, page, "bank-b-balance"), 25)
	if got, want := pageSteps(page), []string{"requested", "debit_committed", "credit_requested", "credit_committed", "finished"}; !slices.Equal(got, want) {
		t.Errorf("page history steps = %q, want %q", got, want)
	}
	for step, want := range map[string]string{"debit_committed": "100 → 75", "credit_requested": "", "credit_committed": "0 → 25"} {
		if got := pageBalanceChange(page, step); got != want {
			t.Errorf("page %s balance change = %q, want %q", step, got, want)
		}
	}

	before := demo.transfer(t, id)
	for range 3 {
		demo.get(t, transferPage)
		demo.get(t, "/api/transfers/"+id)
	}

	transfers := demo.transfers(t)
	if len(transfers) != 1 || transfers[0].TransferID != id {
		t.Errorf("transfers = %+v, want only %s", transfers, id)
	}
	if after := demo.transfer(t, id); !reflect.DeepEqual(after, before) {
		t.Errorf("transfer changed on refresh:\nbefore %+v\nafter  %+v", before, after)
	}
	demo.assertBalances(t, 75, 25)
}

func (d *visitorClient) awaitPage(t *testing.T, path, status string) []byte {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		page := d.get(t, path)
		current, evidence := pageData(page, "transfer-status"), pageData(page, "transfer-evidence")
		polling := regexp.MustCompile(`<meta http-equiv="refresh"`).Match(page)
		if pending := current == "debit_pending" || current == "credit_pending"; pending != (evidence == "") {
			t.Fatalf("status %q shown with evidence %q", current, evidence)
		}
		if polling != (evidence != "complete") {
			t.Fatalf("status %q and evidence %q shown with polling = %t", current, evidence, polling)
		}
		if current == status {
			return page
		}
		if time.Now().After(deadline) {
			t.Fatalf("page status = %q after test deadline, want %q", current, status)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func pageData(page []byte, id string) string {
	match := regexp.MustCompile(`id="` + id + `"[^>]*value="([^"]*)"`).FindSubmatch(page)
	if match == nil {
		return ""
	}
	return string(match[1])
}

func pageBalanceChange(page []byte, step string) string {
	row := regexp.MustCompile(`data-step="` + step + `"(?s:.*?)</tr>`).Find(page)
	match := regexp.MustCompile(`<span class="dim balance"> · ([^<]*)</span>`).FindSubmatch(row)
	if match == nil {
		return ""
	}
	return string(match[1])
}

func pageSteps(page []byte) []string {
	var steps []string
	for _, match := range regexp.MustCompile(`data-step="([^"]*)"`).FindAllSubmatch(page, -1) {
		steps = append(steps, string(match[1]))
	}
	return steps
}
