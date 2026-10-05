package transferservice

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

var lanes = []string{messaging.TransferService, messaging.BankA, messaging.BankB}

type historyRow struct {
	historyEntry
	Lane       int
	Cause      string
	Attempt    int
	About      string
	AboutCause bool
	Tooltip    string
	Label      string
	Continues  bool
	Balance    string
}

func historyRows(history []historyEntry) []historyRow {
	topics := messageTopics(history)
	ackLost := map[string]bool{}
	var confirmedAt, requestedAt time.Time
	for _, e := range history {
		if e.Step == requested {
			requestedAt = e.ObservedAt
		}
		if e.Observation == creditConfirmed {
			confirmedAt = e.ObservedAt
		}
		if e.Observation == nackRequested {
			ackLost[topics[e.CausationID]] = true
		}
	}
	attempts := map[string][]string{}
	var rows []historyRow
	for _, e := range history {
		if e.Observation == deliveryPaused {
			continue
		}
		row := historyRow{historyEntry: e, Lane: slices.Index(lanes, e.Service), Cause: topics[e.CausationID], About: about(e, ackLost), Label: e.Observation.Label(), Balance: balanceChange(e)}
		if e.Observation == admitted && !requestedAt.IsZero() {
			row.Label = fmt.Sprintf("admitted after waiting %.1f s for another visitor’s demo", e.ObservedAt.Sub(requestedAt).Seconds())
		}
		if e.Observation == creditConfirmed && row.Cause != "" {
			row.Cause = "→ [Sent " + row.Cause + "]"
			row.AboutCause = true
		}
		if e.Observation == deliveryResumed && !confirmedAt.IsZero() {
			row.Label = fmt.Sprintf("Command delivered after %.1fs", e.ObservedAt.Sub(confirmedAt).Seconds())
		}
		var ids []string
		if e.CausationID != "" {
			ids = append(ids, "message "+e.CausationID)
		}
		if e.AttemptID != "" {
			ids = append(ids, "attempt "+e.AttemptID)
		}
		row.Tooltip = strings.Join(ids, "\n")
		if e.AttemptID != "" {
			row.Continues = len(rows) > 0 && rows[len(rows)-1].AttemptID == e.AttemptID
			n := slices.Index(attempts[e.CausationID], e.AttemptID)
			if n < 0 {
				attempts[e.CausationID] = append(attempts[e.CausationID], e.AttemptID)
				n = len(attempts[e.CausationID]) - 1
			}
			row.Attempt = n + 1
		}
		rows = append(rows, row)
		if e.Observation == creditConfirmed {
			rows = append(rows, historyRow{
				historyEntry: historyEntry{Observation: deliveryWaiting, Service: messaging.BankB},
				Lane:         slices.Index(lanes, messaging.BankB),
				Label:        deliveryWaiting.Label(),
				About:        "A missing consumer simulates Bank B being down. The credit command waits in the broker's queue with no consumer, neither delivered nor failed.",
			})
		}
	}
	return rows
}

func balanceChange(e historyEntry) string {
	switch {
	case e.BalanceBefore == nil || e.BalanceAfter == nil:
		return ""
	case *e.BalanceBefore == *e.BalanceAfter:
		return fmt.Sprint(*e.BalanceAfter)
	}
	return fmt.Sprintf("%d → %d", *e.BalanceBefore, *e.BalanceAfter)
}

func about(e historyEntry, ackLost map[string]bool) string {
	switch {
	case e.Observation == creditConfirmed:
		return "The broker has the message for Bank B."
	case e.Observation == nackRequested:
		return "We’re simulating the effects of a crash by requesting a redelivery (a Nack), and the broker delivers the message again."
	case e.Step == creditRequested && ackLost[messaging.DebitFundsTopic]:
		return "The debit command, although unacknowledged in order to trigger redelivery, was successful in terms of the commit to Bank A's outbox. The relay then published the result (message to Transfer Service), allowing the flow to continue."
	case e.Step == refundCommitted:
		return "The refund is a new operation, not a rollback the debit."
	case e.Step == transferRefunded && ackLost[messaging.RefundFundsTopic]:
		return "Bank A's FundsRefunded went out through its outbox although the acknowledgement of the refund command was lost, so the flow continued."
	}
	return ""
}

func messageTopics(history []historyEntry) map[string]string {
	topics := map[string]string{}
	for _, e := range history {
		if e.Observation == admitted {
			topics[e.IssuedMessageID] = messaging.DebitFundsTopic
		}
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
	delete(topics, "")
	return topics
}
