package transferservice

import (
	"errors"
	"slices"
	"strings"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

type scenario string

const (
	happyPath        scenario = "happy_path"
	debitRedelivery  scenario = messaging.DebitRedelivery
	creditRejection  scenario = messaging.CreditRejection
	refundRedelivery scenario = messaging.RefundRedelivery
)

var scenarios = []scenario{happyPath, debitRedelivery, creditRejection, refundRedelivery}

var errUnknownScenario = errors.New("scenario must be one of: " + strings.Join(slugs(scenarios), ", "))

func slugs(ss []scenario) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}
	return out
}

func (s scenario) Label() string {
	switch s {
	case happyPath:
		return "Happy path"
	case debitRedelivery:
		return "Debit redelivery"
	case creditRejection:
		return "Credit rejection"
	case refundRedelivery:
		return "Refund redelivery"
	}
	return string(s)
}

func parseScenario(slug string) (scenario, error) {
	if !slices.Contains(scenarios, scenario(slug)) {
		return "", errUnknownScenario
	}
	return scenario(slug), nil
}
