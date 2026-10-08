package acceptance_test

import (
	"cmp"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestCompensationTraceCarriesScenarioMarkers(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)
	tempo := "http://" + serviceAddress(t, demo.project, "tempo", "3200")

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "credit_rejection"}`)
	refunded := demo.awaitTransfer(t, accepted.TransferID, "refunded")
	creditFunds := entry(t, refunded.History, "credit_requested").IssuedMessageID
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, _ := fetchTrace(t, tempo, refunded.TraceID)
		events := consumerSpanEvents(t, body, "bank-b", creditFunds)
		if scenario, found := events["credit.rejected"]; found {
			if scenario != "credit_rejection" {
				t.Errorf("credit.rejected saga.scenario = %q, want credit_rejection", scenario)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s has no credit.rejected event on Bank B's CreditFunds consumer span for %s after test deadline; events %v", refunded.TraceID, creditFunds, events)
		}
		time.Sleep(time.Second)
	}

	accepted = demo.submitTransfer(t, `{"amount": 25, "scenario": "refund_redelivery"}`)
	refunded = demo.awaitReadiness(t, accepted.TransferID, "refunded")
	refundFunds := entry(t, refunded.History, "refund_requested").IssuedMessageID
	deadline = time.Now().Add(30 * time.Second)
	for {
		_, spans := fetchTrace(t, tempo, refunded.TraceID)
		var attempts []exportedSpan
		for _, s := range spans {
			if s.Service == "bank-a" && s.Kind == "SPAN_KIND_CONSUMER" && s.Topic == "RefundFunds" && s.MessageID == refundFunds {
				attempts = append(attempts, s)
			}
		}
		if len(attempts) >= 2 {
			slices.SortFunc(attempts, func(a, b exportedSpan) int { return cmp.Compare(a.Start, b.Start) })
			first, second := attempts[0], attempts[1]
			if len(attempts) != 2 {
				t.Errorf("Bank A RefundFunds consumer spans = %d, want 2: %+v", len(attempts), attempts)
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
			t.Fatalf("trace %s has %d Bank A RefundFunds consumer spans for %s after test deadline, want 2", refunded.TraceID, len(attempts), refundFunds)
		}
		time.Sleep(time.Second)
	}
}

func consumerSpanEvents(t *testing.T, trace []byte, service, messageID string) map[string]string {
	t.Helper()
	if len(trace) == 0 {
		return nil
	}
	var exported struct {
		Trace struct {
			ResourceSpans []struct {
				Resource   struct{ Attributes []otlpAttribute }
				ScopeSpans []struct {
					Spans []struct {
						Kind       string
						Attributes []otlpAttribute
						Events     []struct {
							Name       string
							Attributes []otlpAttribute
						}
					}
				}
			}
		}
	}
	if err := json.Unmarshal(trace, &exported); err != nil {
		t.Fatalf("decode Tempo trace %q: %v", trace, err)
	}
	events := map[string]string{}
	for _, resource := range exported.Trace.ResourceSpans {
		if attributeValue(resource.Resource.Attributes, "service.name") != service {
			continue
		}
		for _, scope := range resource.ScopeSpans {
			for _, s := range scope.Spans {
				if s.Kind != "SPAN_KIND_CONSUMER" || attributeValue(s.Attributes, "messaging.message.id") != messageID {
					continue
				}
				for _, e := range s.Events {
					events[e.Name] = attributeValue(e.Attributes, "saga.scenario")
				}
			}
		}
	}
	return events
}
