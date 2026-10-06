package acceptance_test

import (
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
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

	page := demo.awaitReplay(t, transferPage)
	if got := pageData(page, "transfer-status"); got != "completed" {
		t.Errorf("page status = %q, want completed", got)
	}
	if got := pageData(page, "transfer-id"); got != id {
		t.Errorf("page transfer ID = %q, want %q", got, id)
	}
	if got, want := pageSteps(page), []string{"requested", "debit_committed", "credit_requested", "credit_committed", "finished"}; !slices.Equal(got, want) {
		t.Errorf("page history steps = %q, want %q", got, want)
	}
	for step, want := range map[string]string{"debit_committed": "100 → 75", "credit_requested": "", "credit_committed": "0 → 25"} {
		if got := pageBalanceChange(page, step); got != want {
			t.Errorf("page %s balance change = %q, want %q", step, got, want)
		}
	}

	for row, want := range map[string]string{
		"bank-a":                "Bank A 100 → 75 credits",
		"bank-b":                "Bank B 0 → 25 credits",
		"debit":                 "Debit 1 attempt, 1 effect",
		"credit":                "Credit 1 attempt, 1 effect",
		"refund":                "",
		"duplicates-suppressed": "Duplicates suppressed 0",
	} {
		if got := pageOutcomeSummary(page, row); got != want {
			t.Errorf("page outcome %s = %q, want %q", row, got, want)
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
	return d.awaitPageUntil(t, path, func(page []byte) bool { return pageData(page, "transfer-status") == status })
}

func (d *visitorClient) awaitReplay(t *testing.T, path string) []byte {
	t.Helper()
	return d.awaitPageUntil(t, path, func(page []byte) bool { return pageData(page, "transfer-replay") == "ready" })
}

func (d *visitorClient) awaitPageUntil(t *testing.T, path string, done func([]byte) bool) []byte {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		page := d.get(t, path)
		current, replay := pageData(page, "transfer-status"), pageData(page, "transfer-replay")
		polling := regexp.MustCompile(`<meta http-equiv="refresh"`).Match(page)
		playback := regexp.MustCompile(`id="playback"`).Match(page)
		pending := current == "awaiting_admission" || current == "debit_pending" || current == "credit_pending" || current == "refund_pending"
		if pending != (replay == "pending") || !slices.Contains([]string{"pending", "preparing", "ready"}, replay) {
			t.Fatalf("status %q shown with replay %q", current, replay)
		}
		if polling == (replay == "ready") || playback != (replay == "ready") {
			t.Fatalf("status %q and replay %q shown with polling = %t, playback = %t", current, replay, polling, playback)
		}
		if polling && !regexp.MustCompile(`<meta http-equiv="refresh" content="1; url=/transfers/[^"?]+">`).Match(page) {
			t.Fatalf("polling page does not refresh itself: %s", page)
		}
		if summarised := regexp.MustCompile(`id="outcome"`).Match(page); summarised != (replay == "ready") {
			t.Fatalf("status %q and replay %q shown with outcome summary = %t", current, replay, summarised)
		}
		if replay == "ready" && !regexp.MustCompile(`<tr data-step="requested" data-observed-at="[^"]+" data-dwell="\d+"`).Match(page) {
			t.Fatalf("ready page rows carry no playback timing: %s", page)
		}
		if done(page) {
			return page
		}
		if time.Now().After(deadline) {
			t.Fatalf("page status = %q, replay = %q after test deadline", current, replay)
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

func pageOutcomeSummary(page []byte, row string) string {
	segment := regexp.MustCompile(`data-outcome="` + row + `"[^>]*>(?s:(.*?))</(?:div|li)>`).FindSubmatch(page)
	if segment == nil {
		return ""
	}
	return pageText(segment[1])
}

func pageText(markup []byte) string {
	text := regexp.MustCompile(`<[^>]*>`).ReplaceAll(markup, []byte(" "))
	return strings.Join(strings.Fields(string(text)), " ")
}

func pageElementText(page []byte, id string) string {
	match := regexp.MustCompile(`id="` + id + `"[^>]*>([^<]*)<`).FindSubmatch(page)
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
