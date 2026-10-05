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
}

func historyRows(history []historyEntry) []historyRow {
	history = slices.Clone(history)
	slices.SortStableFunc(history, func(a, b historyEntry) int { return a.ObservedAt.Compare(b.ObservedAt) })
	topics := messageTopics(history)
	attempts := map[string][]string{}
	rows := make([]historyRow, len(history))
	for i, e := range history {
		rows[i] = historyRow{historyEntry: e, Lane: slices.Index(lanes, e.Service), Cause: topics[e.CausationID]}
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

func messageTopics(history []historyEntry) map[string]string {
	topics := map[string]string{}
	for _, e := range history {
		switch e.Step {
		case requested:
			topics[e.IssuedMessageID] = messaging.DebitFundsTopic
		case debitCommitted:
			topics[e.MessageID] = messaging.FundsDebitedTopic
			topics[e.IssuedMessageID] = messaging.CreditFundsTopic
		case debitRejected:
			topics[e.MessageID] = messaging.DebitRejectedTopic
		case creditCommitted:
			topics[e.MessageID] = messaging.FundsCreditedTopic
		}
	}
	return topics
}
