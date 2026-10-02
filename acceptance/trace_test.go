package acceptance_test

import (
	"encoding/json"
	"io"
	"net/http"
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
	assertSteps(t, completed.History, "requested", "debit_committed", "credit_committed", "finished")
	debitFunds := completed.History[0].IssuedMessageID
	fundsDebited := completed.History[1].MessageID
	creditFunds := completed.History[1].IssuedMessageID
	fundsCredited := completed.History[2].MessageID

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
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, spans := fetchTrace(t, tempo, completed.TraceID)
		missing := missingSpans(spans, want)
		if len(missing) == 0 {
			assertNoSecrets(t, body)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s lacks spans after test deadline: %+v\nexported spans: %+v", completed.TraceID, missing, spans)
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

func missingSpans(spans map[span]bool, want []span) []span {
	var missing []span
	for _, s := range want {
		if !spans[s] {
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

func fetchTrace(t *testing.T, tempo, traceID string) ([]byte, map[span]bool) {
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
						Kind       string
						Attributes []otlpAttribute
					}
				}
			}
		}
	}
	if err := json.Unmarshal(body, &exported); err != nil {
		t.Fatalf("decode Tempo trace %q: %v", body, err)
	}
	spans := map[span]bool{}
	for _, resource := range exported.Trace.ResourceSpans {
		service := attributeValue(resource.Resource.Attributes, "service.name")
		for _, scope := range resource.ScopeSpans {
			for _, s := range scope.Spans {
				spans[span{
					Service:     service,
					Kind:        s.Kind,
					Topic:       attributeValue(s.Attributes, "messaging.destination.name"),
					MessageID:   attributeValue(s.Attributes, "messaging.message.id"),
					CausationID: attributeValue(s.Attributes, "saga.causation_id"),
					TransferID:  attributeValue(s.Attributes, "saga.transfer_id"),
					Database:    attributeValue(s.Attributes, "db.system.name") == "postgresql",
				}] = true
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
