package transferservice

import (
	"slices"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

type outcome struct {
	Balances             outcomeBalances `json:"balances"`
	Commands             outcomeCommands `json:"commands"`
	DuplicatesSuppressed int             `json:"duplicates_suppressed"`
	DuplicateEffects     int             `json:"duplicate_effects"`
}

type outcomeBalances struct {
	BankA balancePair     `json:"bank_a"`
	BankB destinationPair `json:"bank_b"`
}

type balancePair struct {
	Before *int64 `json:"before"`
	After  *int64 `json:"after"`
}

type destinationPair struct {
	balancePair
	Involved bool `json:"involved"`
}

type outcomeCommands struct {
	Debit  *commandCount `json:"debit"`
	Credit *commandCount `json:"credit"`
	Refund *commandCount `json:"refund"`
}

type commandCount struct {
	Attempts int `json:"attempts"`
	Effects  int `json:"effects"`
}

var committedSteps = []step{debitCommitted, creditCommitted, refundCommitted}

func deriveOutcome(history []historyEntry) outcome {
	o := outcome{Balances: outcomeBalances{BankB: destinationPair{Involved: true}}}
	topics := messageTopics(history)
	counts := map[string]*commandCount{}
	for _, topic := range topics {
		switch topic {
		case messaging.DebitFundsTopic:
			o.Commands.Debit = &commandCount{}
			counts[topic] = o.Commands.Debit
		case messaging.CreditFundsTopic:
			o.Commands.Credit = &commandCount{}
			counts[topic] = o.Commands.Credit
		case messaging.RefundFundsTopic:
			o.Commands.Refund = &commandCount{}
			counts[topic] = o.Commands.Refund
		}
	}
	attempts := map[string]bool{}
	for _, e := range history {
		switch e.Step {
		case debitCommitted, debitRejected:
			if o.Balances.BankA.Before == nil {
				o.Balances.BankA.Before = e.BalanceBefore
			}
		case creditCommitted, creditRejected:
			o.Balances.BankB.balancePair = balancePair{e.BalanceBefore, e.BalanceAfter}
		}
		if e.Step == debitRejected {
			o.Balances.BankB.Involved = false
		}
		if slices.Contains([]step{debitCommitted, debitRejected, refundCommitted}, e.Step) && e.BalanceBefore != nil && e.BalanceAfter != nil {
			o.Balances.BankA.After = e.BalanceAfter
		}
		count := counts[topics[e.CausationID]]
		if count == nil {
			continue
		}
		if e.AttemptID != "" && !attempts[e.AttemptID] {
			attempts[e.AttemptID] = true
			count.Attempts++
		}
		if slices.Contains(committedSteps, e.Step) {
			count.Effects++
		}
		if e.Observation == duplicateSuppressed {
			o.DuplicatesSuppressed++
		}
	}
	for _, count := range counts {
		o.DuplicateEffects += max(count.Effects-1, 0)
	}
	return o
}
