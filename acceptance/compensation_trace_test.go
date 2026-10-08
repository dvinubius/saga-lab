package acceptance_test

import (
	"testing"
)

func TestCompensationTraceCarriesScenarioMarkers(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)
	tempo := "http://" + serviceAddress(t, demo.project, "tempo", "3200")

	accepted := demo.submitTransfer(t, `{"amount": 25, "scenario": "credit_rejection"}`)
	refunded := demo.awaitTransfer(t, accepted.TransferID, "refunded")
	creditFunds := entry(t, refunded.History, "credit_requested").IssuedMessageID
	var rejection spanEvent
	awaitSpans(t, tempo, refunded, func(spans []exportedSpan) bool {
		for _, s := range spans {
			if s.Service == "bank-b" && s.Kind == "SPAN_KIND_CONSUMER" && s.MessageID == creditFunds {
				if e, found := s.event("credit.rejected"); found {
					rejection = e
					return true
				}
			}
		}
		return false
	})
	if scenario := attributeValue(rejection.Attributes, "saga.scenario"); scenario != "credit_rejection" {
		t.Errorf("credit.rejected saga.scenario = %q, want credit_rejection", scenario)
	}

	accepted = demo.submitTransfer(t, `{"amount": 25, "scenario": "refund_redelivery"}`)
	refunded = demo.awaitReadiness(t, accepted.TransferID, "refunded")
	assertRedeliveryTrace(t, tempo, refunded, "RefundFunds", entry(t, refunded.History, "refund_requested").IssuedMessageID)
}
