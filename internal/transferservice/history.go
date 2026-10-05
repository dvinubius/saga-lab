package transferservice

import (
	"slices"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

var lanes = []string{messaging.TransferService, messaging.BankA, messaging.BankB}

type historyRow struct {
	historyEntry
	Lane    int
	Cause   string
	Attempt int
	About   string
}

func historyRows(history []historyEntry) []historyRow {
	topics := messageTopics(history)
	ackLost := map[string]bool{}
	for _, e := range history {
		if e.Observation == nackRequested {
			ackLost[topics[e.CausationID]] = true
		}
	}
	attempts := map[string][]string{}
	rows := make([]historyRow, len(history))
	for i, e := range history {
		rows[i] = historyRow{historyEntry: e, Lane: slices.Index(lanes, e.Service), Cause: topics[e.CausationID], About: about(e, ackLost)}
		if e.AttemptID == "" {
			continue
		}
		n := slices.Index(attempts[e.CausationID], e.AttemptID)
		if n < 0 {
			attempts[e.CausationID] = append(attempts[e.CausationID], e.AttemptID)
			n = len(attempts[e.CausationID]) - 1
		}
		rows[i].Attempt = n + 1
	}
	return rows
}

func about(e historyEntry, ackLost map[string]bool) string {
	switch {
	case e.Observation == nackRequested:
		return "We’re simulating the effects of a crash by requesting a redelivery (a Nack), and the broker delivers the message again."
	case e.Step == creditRequested && ackLost[messaging.DebitFundsTopic]:
		return "The debit command, although unacknowledged in order to trigger redelivery, was successful in terms of the commit to Bank A's outbox. The relay then published the result (message to Transfer Service), allowing the flow to continue."
	case e.Step == refundCommitted:
		return "The refund is a new operation at Bank A that restores the source balance, not a rollback of Bank A's debit."
	case e.Step == transferRefunded && ackLost[messaging.RefundFundsTopic]:
		return "Bank A's FundsRefunded went out through its outbox although the acknowledgement of the refund command was lost, so the flow continued."
	}
	return ""
}

func messageTopics(history []historyEntry) map[string]string {
	topics := map[string]string{}
	for _, e := range history {
		switch e.Step {
		case requested:
			topics[e.IssuedMessageID] = messaging.DebitFundsTopic
		case debitCommitted:
			topics[e.MessageID] = messaging.FundsDebitedTopic
		case creditRequested:
			topics[e.IssuedMessageID] = messaging.CreditFundsTopic
		case debitRejected:
			topics[e.MessageID] = messaging.DebitRejectedTopic
		case creditCommitted:
			topics[e.MessageID] = messaging.FundsCreditedTopic
		case creditRejected:
			topics[e.MessageID] = messaging.CreditRejectedTopic
		case refundRequested:
			topics[e.IssuedMessageID] = messaging.RefundFundsTopic
		case refundCommitted:
			topics[e.MessageID] = messaging.FundsRefundedTopic
		}
	}
	return topics
}
