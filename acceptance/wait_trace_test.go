package acceptance_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestBankBUnavailabilityTraceLabelsItsWaits(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)
	holder := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	second := demo.visitor(t)
	queued := second.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if holder.Status != "debit_pending" || queued.Status != "awaiting_admission" {
		t.Fatalf("holder = %+v, queued = %+v", holder, queued)
	}
	completedHolder := demo.awaitReadiness(t, holder.TransferID, "completed")
	completedQueued := second.awaitReadiness(t, queued.TransferID, "completed")
	tempo := "http://" + serviceAddress(t, demo.project, "tempo", "3200")

	creditFunds := entry(t, completedQueued.History, "credit_requested").IssuedMessageID
	dedicatedCredit := func(s namedSpan) bool {
		return s.Service == "bank-b" && s.Kind == "SPAN_KIND_CONSUMER" && s.Topic == "CreditFundsDedicated" && s.MessageID == creditFunds
	}
	queuedSpans := awaitSpans(t, tempo, completedQueued, func(spans []namedSpan) bool {
		return len(waitSpans(spans, completedQueued.TransferID, deliveryWaitSpan)) != 0 && slices.ContainsFunc(spans, dedicatedCredit)
	})
	admissions := observations(completedQueued.History, "Admitted")
	if len(admissions) != 1 {
		t.Fatalf("admissions = %+v, want one", admissions)
	}
	admissionWaits := waitSpans(queuedSpans, completedQueued.TransferID, admissionWaitSpan)
	if len(admissionWaits) != 1 {
		t.Fatalf("admission wait spans = %+v, want one", admissionWaits)
	}
	assertNear(t, "admission wait start", admissionWaits[0].Start, entry(t, completedQueued.History, "requested").ObservedAt)
	assertNear(t, "admission wait end", admissionWaits[0].End, admissions[0].ObservedAt)
	assertDeliveryWait(t, queuedSpans, completedQueued)
	if !slices.ContainsFunc(queuedSpans, func(s namedSpan) bool { return dedicatedCredit(s) && slices.Contains(s.Events, "consumer.paused") }) {
		t.Errorf("no Bank B dedicated credit span for %s with consumer.paused: %+v", creditFunds, queuedSpans)
	}

	holderSpans := awaitSpans(t, tempo, completedHolder, func(spans []namedSpan) bool {
		return len(waitSpans(spans, completedHolder.TransferID, deliveryWaitSpan)) != 0
	})
	assertDeliveryWait(t, holderSpans, completedHolder)
	if waits := waitSpans(holderSpans, completedHolder.TransferID, admissionWaitSpan); len(waits) != 0 {
		t.Errorf("transfer admitted at submission has admission wait spans: %+v", waits)
	}
}

const (
	admissionWaitSpan = "admission wait"
	deliveryWaitSpan  = "delivery wait (scheduled)"
)

type namedSpan struct {
	Service    string
	Name       string
	Kind       string
	Topic      string
	MessageID  string
	TransferID string
	Start      time.Time
	End        time.Time
	Events     []string
}

func awaitSpans(t *testing.T, tempo string, tr transfer, exported func([]namedSpan) bool) []namedSpan {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		body, _ := fetchTrace(t, tempo, tr.TraceID)
		spans := namedSpans(body)
		if exported(spans) {
			return spans
		}
		if time.Now().After(deadline) {
			t.Fatalf("trace %s of transfer %s lacks expected spans after test deadline: %+v", tr.TraceID, tr.TransferID, spans)
		}
		time.Sleep(time.Second)
	}
}

func assertDeliveryWait(t *testing.T, spans []namedSpan, tr transfer) {
	t.Helper()
	waits := waitSpans(spans, tr.TransferID, deliveryWaitSpan)
	if len(waits) != 1 {
		t.Fatalf("delivery wait spans = %+v, want one", waits)
	}
	wait := waits[0]
	assertNear(t, "delivery wait start", wait.Start, entry(t, tr.History, "credit_requested").ObservedAt)
	resumed := observations(tr.History, "DeliveryResumed")
	if len(resumed) != 1 {
		t.Fatalf("resumed = %+v, want one", resumed)
	}
	if wait.End.Sub(wait.Start) < 2500*time.Millisecond || wait.End.After(resumed[0].ObservedAt) {
		t.Errorf("delivery wait %v – %v, want at least 2.5 s ending before Bank B resumed at %v", wait.Start, wait.End, resumed[0].ObservedAt)
	}
}

func waitSpans(spans []namedSpan, transferID, name string) []namedSpan {
	var found []namedSpan
	for _, s := range spans {
		if s.Service == "transfer-service" && s.Name == name && s.TransferID == transferID {
			found = append(found, s)
		}
	}
	return found
}

func assertNear(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if d := got.Sub(want).Abs(); d > time.Millisecond {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func namedSpans(body []byte) []namedSpan {
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
						Events            []struct{ Name string }
					}
				}
			}
		}
	}
	if err := json.Unmarshal(body, &exported); err != nil {
		return nil
	}
	var spans []namedSpan
	for _, resource := range exported.Trace.ResourceSpans {
		service := attributeValue(resource.Resource.Attributes, "service.name")
		for _, scope := range resource.ScopeSpans {
			for _, s := range scope.Spans {
				var events []string
				for _, e := range s.Events {
					events = append(events, e.Name)
				}
				spans = append(spans, namedSpan{
					Service:    service,
					Name:       s.Name,
					Kind:       s.Kind,
					Topic:      attributeValue(s.Attributes, "messaging.destination.name"),
					MessageID:  attributeValue(s.Attributes, "messaging.message.id"),
					TransferID: attributeValue(s.Attributes, "saga.transfer_id"),
					Start:      time.Unix(0, s.StartTimeUnixNano),
					End:        time.Unix(0, s.EndTimeUnixNano),
					Events:     events,
				})
			}
		}
	}
	return spans
}
