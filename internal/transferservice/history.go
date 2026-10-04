package transferservice

import (
	"slices"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

type historyRow struct {
	historyEntry
	Cause   string
	Attempt int
}

func historyRows(history []historyEntry) []historyRow {
	names := map[string]string{}
	for _, e := range history {
		switch e.Step {
		case requested:
			names[e.IssuedMessageID] = messaging.DebitFundsTopic
		case debitCommitted:
			names[e.MessageID] = messaging.FundsDebitedTopic
			names[e.IssuedMessageID] = messaging.CreditFundsTopic
		case debitRejected:
			names[e.MessageID] = messaging.DebitRejectedTopic
		case creditCommitted:
			names[e.MessageID] = messaging.FundsCreditedTopic
		}
	}
	attempts := map[string][]string{}
	rows := make([]historyRow, len(history))
	for i, e := range history {
		rows[i] = historyRow{historyEntry: e, Cause: names[e.CausationID]}
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
