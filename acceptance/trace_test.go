package acceptance_test

import (
	"cmp"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/visitor"
)

func TestTransferTraceCoversAllServices(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25}`)
	visitorURL, err := url.Parse(demo.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	visitorID := ""
	for _, cookie := range demo.client.Jar.Cookies(visitorURL) {
		if cookie.Name == visitor.CookieName {
			visitorID = cookie.Value
		}
	}
	if visitorID == "" {
		t.Fatal("visitor cookie missing")
	}
	completed := demo.awaitTransfer(t, accepted.TransferID, "completed")
	if completed.TraceID == "" {
		t.Fatal("completed transfer has no trace ID")
	}
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_requested", "credit_committed", "finished")
	debitFunds := completed.History[0].IssuedMessageID
	fundsDebited := completed.History[1].MessageID
	creditFunds := completed.History[2].IssuedMessageID
	fundsCredited := completed.History[3].MessageID

	message := func(service, kind, topic, messageID, causationID string) span {
		return span{Service: service, Kind: kind, Topic: topic, MessageID: messageID, CausationID: causationID, TransferID: accepted.TransferID}
	}
	want := []span{
		{Service: "transfer-service", Kind: "SPAN_KIND_SERVER"},
		{Service: "transfer-service", Kind: "SPAN_KIND_CLIENT"},
		{Service: "bank-a", Kind: "SPAN_KIND_SERVER"},
		{Service: "bank-b", Kind: "SPAN_KIND_SERVER"},
		{Service: "transfer-service", Kind: "SPAN_KIND_CLIENT", Database: true},
		{Service: "bank-a", Kind: "SPAN_KIND_CLIENT", Database: true},
		{Service: "bank-b", Kind: "SPAN_KIND_CLIENT", Database: true},
		message("transfer-service", "SPAN_KIND_PRODUCER", "DebitFunds", debitFunds, ""),
		message("bank-a", "SPAN_KIND_CONSUMER", "DebitFunds", debitFunds, ""),
		message("bank-a", "SPAN_KIND_PRODUCER", "FundsDebited", fundsDebited, debitFunds),
		message("transfer-service", "SPAN_KIND_CONSUMER", "FundsDebited", fundsDebited, debitFunds),
		message("transfer-service", "SPAN_KIND_PRODUCER", "CreditFunds", creditFunds, fundsDebited),
		message("bank-b", "SPAN_KIND_CONSUMER", "CreditFunds", creditFunds, fundsDebited),
		message("bank-b", "SPAN_KIND_PRODUCER", "FundsCredited", fundsCredited, creditFunds),
		message("transfer-service", "SPAN_KIND_CONSUMER", "FundsCredited", fundsCredited, creditFunds),
	}

	tempo := "http://" + serviceAddress(t, demo.project, "tempo", "3200")
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, spans := fetchTrace(t, tempo, completed.TraceID)
		missing := missingSpans(spans, want)
		if len(missing) == 0 {
			assertNoSecrets(t, body, visitorID)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s lacks spans after test deadline: %+v\nexported spans: %+v", completed.TraceID, missing, spans)
		}
		time.Sleep(time.Second)
	}

	redelivered := demo.submitTransfer(t, `{"amount": 25, "scenario": "debit_redelivery"}`)
	completed = demo.awaitTransfer(t, redelivered.TransferID, "completed")
	debitFunds = completed.History[0].IssuedMessageID
	deadline = time.Now().Add(30 * time.Second)
	for {
		body, spans := fetchTrace(t, tempo, completed.TraceID)
		var attempts []exportedSpan
		for _, s := range spans {
			if s.Service == "bank-a" && s.Kind == "SPAN_KIND_CONSUMER" && s.Topic == "DebitFunds" && s.MessageID == debitFunds {
				attempts = append(attempts, s)
			}
		}
		if len(attempts) >= 2 {
			assertNoSecrets(t, body, visitorID)
			slices.SortFunc(attempts, func(a, b exportedSpan) int { return cmp.Compare(a.Start, b.Start) })
			first, second := attempts[0], attempts[1]
			if len(attempts) != 2 {
				t.Errorf("Bank A DebitFunds consumer spans = %d, want 2: %+v", len(attempts), attempts)
			}
			if first.AttemptID == "" || first.AttemptID == second.AttemptID {
				t.Errorf("attempt IDs = %q and %q, want two different IDs", first.AttemptID, second.AttemptID)
			}
			if !first.Failed || !slices.Contains(first.Events, "fault.injected") {
				t.Errorf("first attempt failed = %v, events = %q, want error status and fault.injected", first.Failed, first.Events)
			}
			if second.Failed || !slices.Contains(second.Events, "duplicate.suppressed") {
				t.Errorf("second attempt failed = %v, events = %q, want no error status and duplicate.suppressed", second.Failed, second.Events)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s has %d Bank A DebitFunds consumer spans for %s after test deadline, want 2", completed.TraceID, len(attempts), debitFunds)
		}
		time.Sleep(time.Second)
	}
}

type span struct {
	Service     string
	Kind        string
	Topic       string
	MessageID   string
	CausationID string
	TransferID  string
	Database    bool
}

type exportedSpan struct {
	span
	AttemptID string
	Start     uint64
	Failed    bool
	Events    []string
}

func missingSpans(spans []exportedSpan, want []span) []span {
	seen := map[span]bool{}
	for _, s := range spans {
		seen[s.span] = true
	}
	var missing []span
	for _, s := range want {
		if !seen[s] {
			missing = append(missing, s)
		}
	}
	return missing
}

type otlpAttribute struct {
	Key   string
	Value struct{ StringValue string }
}

func attributeValue(attributes []otlpAttribute, key string) string {
	for _, a := range attributes {
		if a.Key == key {
			return a.Value.StringValue
		}
	}
	return ""
}

func fetchTrace(t *testing.T, tempo, traceID string) ([]byte, []exportedSpan) {
	t.Helper()
	r, err := http.Get(tempo + "/api/v2/traces/" + traceID)
	if err != nil {
		t.Fatalf("query Tempo: %v", err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read Tempo response: %v", err)
	}
	if r.StatusCode == http.StatusNotFound {
		return body, nil
	}
	if r.StatusCode != http.StatusOK {
		t.Fatalf("query Tempo: status %d, body %q", r.StatusCode, body)
	}
	var exported struct {
		Trace struct {
			ResourceSpans []struct {
				Resource   struct{ Attributes []otlpAttribute }
				ScopeSpans []struct {
					Spans []struct {
						Kind              string
						StartTimeUnixNano uint64 `json:",string"`
						Attributes        []otlpAttribute
						Status            struct{ Code string }
						Events            []struct{ Name string }
					}
				}
			}
		}
	}
	if err := json.Unmarshal(body, &exported); err != nil {
		t.Fatalf("decode Tempo trace %q: %v", body, err)
	}
	var spans []exportedSpan
	for _, resource := range exported.Trace.ResourceSpans {
		service := attributeValue(resource.Resource.Attributes, "service.name")
		for _, scope := range resource.ScopeSpans {
			for _, s := range scope.Spans {
				var events []string
				for _, e := range s.Events {
					events = append(events, e.Name)
				}
				spans = append(spans, exportedSpan{
					span: span{
						Service:     service,
						Kind:        s.Kind,
						Topic:       attributeValue(s.Attributes, "messaging.destination.name"),
						MessageID:   attributeValue(s.Attributes, "messaging.message.id"),
						CausationID: attributeValue(s.Attributes, "saga.causation_id"),
						TransferID:  attributeValue(s.Attributes, "saga.transfer_id"),
						Database:    attributeValue(s.Attributes, "db.system.name") == "postgresql",
					},
					AttemptID: attributeValue(s.Attributes, "saga.attempt_id"),
					Start:     s.StartTimeUnixNano,
					Failed:    s.Status.Code == "STATUS_CODE_ERROR",
					Events:    events,
				})
			}
		}
	}
	return body, spans
}

func assertNoSecrets(t *testing.T, trace []byte, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(string(trace), secret) {
			t.Error("exported trace contains a visitor cookie value")
		}
	}
	exported := strings.ToLower(string(trace))
	for _, leak := range []string{"postgres://", "amqp://", "cookie", "password", "user.name"} {
		if strings.Contains(exported, leak) {
			t.Errorf("exported trace contains %q", leak)
		}
	}
}
