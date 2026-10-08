package acceptance_test

import (
	"html"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
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

func TestHomePageIntroducesTheDemonstration(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	page := demo.get(t, "/")
	intro := regexp.MustCompile(`id="introduction"[^>]*>(?s:(.*?))</section>`).FindSubmatch(page)
	if intro == nil {
		t.Fatalf("home page has no introduction: %s", page)
	}
	text := html.UnescapeString(pageText(intro[1]))
	for _, want := range []string{"orchestrated Saga", "Bank A", "Bank B", "message-based distributed transaction", "Five scenarios", "replay its execution", "examine the trace", "Grafana"} {
		if !strings.Contains(text, want) {
			t.Errorf("introduction %q does not mention %q", text, want)
		}
	}
	if !regexp.MustCompile(`<footer[^>]*>(?s:.*?)<a [^>]*href="https://github.com/dvinubius/saga-lab"(?s:.*?)</footer>`).Match(page) {
		t.Error("home page footer does not link to the GitHub repository")
	}
}

func TestTransferPagesLinkToTheirTrace(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)

	visitors := map[string]*visitorClient{}
	accepted := map[string]transfer{}
	for _, scenario := range []string{"happy_path", "debit_redelivery", "credit_rejection", "bank_b_unavailable", "refund_redelivery"} {
		visitors[scenario] = demo.visitor(t)
		accepted[scenario] = visitors[scenario].submitTransfer(t, `{"amount":25,"scenario":"`+scenario+`"}`)
	}
	for scenario, v := range visitors {
		id := accepted[scenario].TransferID
		v.awaitReplay(t, "/transfers/"+id)
		page, current := v.settledPage(t, id)
		from := strconv.FormatInt(current.RequestedAt.Add(-10*time.Second).UnixMilli(), 10)
		to := strconv.FormatInt(current.History[len(current.History)-1].ObservedAt.Add(10*time.Second).UnixMilli(), 10)

		trace := pageLink(t, page, "Trace →")
		if trace == nil {
			t.Errorf("%s page has no trace link", scenario)
			continue
		}
		query := trace.Query()
		if trace.Scheme+"://"+trace.Host+trace.Path != "http://localhost:3000/grafana/d/"+traceDashboard || current.TraceID == "" || query.Get("var-traceId") != current.TraceID || query.Get("from") != from || query.Get("to") != to {
			t.Errorf("%s trace link = %s, want the Trace dashboard on trace %q from %s to %s", scenario, trace, current.TraceID, from, to)
		}
	}
}

func (d *visitorClient) settledPage(t *testing.T, id string) ([]byte, transfer) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		before := d.transfer(t, id)
		page := d.get(t, "/transfers/"+id)
		after := d.transfer(t, id)
		if len(before.History) == len(after.History) {
			return page, after
		}
		if time.Now().After(deadline) {
			t.Fatalf("transfer %s history kept changing", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func pageLink(t *testing.T, page []byte, text string) *url.URL {
	t.Helper()
	match := regexp.MustCompile(`<a [^>]*href="([^"]*)"[^>]*>` + regexp.QuoteMeta(text) + `</a>`).FindSubmatch(page)
	if match == nil {
		return nil
	}
	link, err := url.Parse(html.UnescapeString(string(match[1])))
	if err != nil {
		t.Fatalf("parse link %q: %v", match[1], err)
	}
	return link
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
		polling := regexp.MustCompile(`<script src="/static/poll.js"`).Match(page)
		playback := regexp.MustCompile(`id="playback"`).Match(page)
		pending := current == "awaiting_admission" || current == "debit_pending" || current == "credit_pending" || current == "refund_pending"
		if pending != (replay == "pending") || !slices.Contains([]string{"pending", "preparing", "ready"}, replay) {
			t.Fatalf("status %q shown with replay %q", current, replay)
		}
		if polling == (replay == "ready") || playback != (replay == "ready") {
			t.Fatalf("status %q and replay %q shown with polling = %t, playback = %t", current, replay, polling, playback)
		}
		if polling != regexp.MustCompile(`<noscript><meta http-equiv="refresh" content="1; url=/transfers/[^"?]+"></noscript>`).Match(page) {
			t.Fatalf("polling page does not fall back to refreshing itself without scripts: %s", page)
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
