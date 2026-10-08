package acceptance_test

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTransferTraceCoversAllServices(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount": 25}`)
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
	awaitSpans(t, tempo, completed, func(spans []exportedSpan) bool { return len(missingSpans(spans, want)) == 0 })
	body, _ := fetchTrace(t, tempo, completed.TraceID)
	assertNoSecrets(t, body)

	redelivered := demo.submitTransfer(t, `{"amount": 25, "scenario": "debit_redelivery"}`)
	completed = demo.awaitTransfer(t, redelivered.TransferID, "completed")
	assertRedeliveryTrace(t, tempo, completed, "DebitFunds", completed.History[0].IssuedMessageID)
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
	Name      string
	AttemptID string
	Start     time.Time
	End       time.Time
	Failed    bool
	Events    []spanEvent
}

type spanEvent struct {
	Name       string
	Attributes []otlpAttribute
}

func (s exportedSpan) event(name string) (spanEvent, bool) {
	i := slices.IndexFunc(s.Events, func(e spanEvent) bool { return e.Name == name })
	if i < 0 {
		return spanEvent{}, false
	}
	return s.Events[i], true
}

func (s exportedSpan) hasEvent(name string) bool {
	_, found := s.event(name)
	return found
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

func assertRedeliveryTrace(t *testing.T, tempo string, tr transfer, topic, messageID string) {
	t.Helper()
	var attempts []exportedSpan
	awaitSpans(t, tempo, tr, func(spans []exportedSpan) bool {
		attempts = nil
		for _, s := range spans {
			if s.Service == "bank-a" && s.Kind == "SPAN_KIND_CONSUMER" && s.Topic == topic && s.MessageID == messageID {
				attempts = append(attempts, s)
			}
		}
		return len(attempts) >= 2
	})
	slices.SortFunc(attempts, func(a, b exportedSpan) int { return a.Start.Compare(b.Start) })
	if len(attempts) != 2 {
		t.Errorf("Bank A %s consumer spans = %d, want 2: %+v", topic, len(attempts), attempts)
	}
	first, second := attempts[0], attempts[1]
	if first.AttemptID == "" || first.AttemptID == second.AttemptID {
		t.Errorf("attempt IDs = %q and %q, want two different IDs", first.AttemptID, second.AttemptID)
	}
	if !first.Failed || !first.hasEvent("fault.injected") {
		t.Errorf("first attempt failed = %v, events = %+v, want error status and fault.injected", first.Failed, first.Events)
	}
	if second.Failed || !second.hasEvent("duplicate.suppressed") {
		t.Errorf("second attempt failed = %v, events = %+v, want no error status and duplicate.suppressed", second.Failed, second.Events)
	}
}

func awaitSpans(t *testing.T, tempo string, tr transfer, exported func([]exportedSpan) bool) []exportedSpan {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		_, spans := fetchTrace(t, tempo, tr.TraceID)
		if exported(spans) {
			return spans
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s of transfer %s lacks expected spans after test deadline: %+v", tr.TraceID, tr.TransferID, spans)
		}
		time.Sleep(time.Second)
	}
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
						Name              string
						Kind              string
						StartTimeUnixNano int64 `json:",string"`
						EndTimeUnixNano   int64 `json:",string"`
						Attributes        []otlpAttribute
						Status            struct{ Code string }
						Events            []spanEvent
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
					Name:      s.Name,
					AttemptID: attributeValue(s.Attributes, "saga.attempt_id"),
					Start:     time.Unix(0, s.StartTimeUnixNano),
					End:       time.Unix(0, s.EndTimeUnixNano),
					Failed:    s.Status.Code == "STATUS_CODE_ERROR",
					Events:    s.Events,
				})
			}
		}
	}
	return body, spans
}

func assertNoSecrets(t *testing.T, trace []byte) {
	t.Helper()
	exported := strings.ToLower(string(trace))
	for _, leak := range []string{"postgres://", "amqp://", "cookie", "password", "user.name"} {
		if strings.Contains(exported, leak) {
			t.Errorf("exported trace contains %q", leak)
		}
	}
}
