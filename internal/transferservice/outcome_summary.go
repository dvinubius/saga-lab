package transferservice

import (
	"slices"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

type outcomeSummary struct {
	Balances             summaryBalances `json:"balances"`
	Commands             summaryCommands `json:"commands"`
	DuplicatesSuppressed int             `json:"duplicates_suppressed"`
	DuplicateEffects     int             `json:"duplicate_effects"`
}

type summaryBalances struct {
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

type summaryCommands struct {
	Debit  *commandCount `json:"debit"`
	Credit *commandCount `json:"credit"`
	Refund *commandCount `json:"refund"`
}

type commandCount struct {
	Attempts int `json:"attempts"`
	Effects  int `json:"effects"`
}

var (
	commandTopics  = []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic, messaging.RefundFundsTopic}
	committedSteps = []step{debitCommitted, creditCommitted, refundCommitted}
)

func summariseOutcome(history []historyEntry) outcomeSummary {
	s := outcomeSummary{Balances: summaryBalances{BankB: destinationPair{Involved: true}}}
	topics := messageTopics(history)
	counts := map[string]*commandCount{}
	for _, topic := range topics {
		if slices.Contains(commandTopics, topic) {
			counts[topic] = &commandCount{}
		}
	}
	s.Commands = summaryCommands{
		Debit:  counts[messaging.DebitFundsTopic],
		Credit: counts[messaging.CreditFundsTopic],
		Refund: counts[messaging.RefundFundsTopic],
	}
	attempts := map[[2]string]bool{}
	for _, e := range history {
		switch e.Step {
		case debitCommitted, debitRejected:
			if s.Balances.BankA.Before == nil {
				s.Balances.BankA.Before = e.BalanceBefore
			}
		case creditCommitted, creditRejected:
			s.Balances.BankB.balancePair = balancePair{e.BalanceBefore, e.BalanceAfter}
		}
		if e.Step == debitRejected {
			s.Balances.BankB.Involved = false
		}
		if slices.Contains([]step{debitCommitted, debitRejected, refundCommitted}, e.Step) && e.BalanceBefore != nil && e.BalanceAfter != nil {
			s.Balances.BankA.After = e.BalanceAfter
		}
		topic := topics[e.CausationID]
		count := counts[topic]
		if count == nil {
			continue
		}
		attempt := [2]string{topic, e.AttemptID}
		if e.AttemptID != "" && !attempts[attempt] {
			attempts[attempt] = true
			count.Attempts++
		}
		if slices.Contains(committedSteps, e.Step) {
			count.Effects++
		}
		if e.Observation == duplicateSuppressed {
			s.DuplicatesSuppressed++
		}
	}
	for _, count := range counts {
		s.DuplicateEffects += max(count.Effects-1, 0)
	}
	return s
}
