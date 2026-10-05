package transferservice

import (
	"errors"
	"slices"

	"github.com/dvinubius/saga-lab/internal/messaging"
)

type scenario string

const (
	happyPath       scenario = "happy_path"
	debitRedelivery scenario = messaging.DebitRedelivery
	creditRejection scenario = messaging.CreditRejection
)

var scenarios = []scenario{happyPath, debitRedelivery, creditRejection}

var errUnknownScenario = errors.New("scenario must be one of: happy_path, debit_redelivery, credit_rejection")

func (s scenario) Label() string {
	switch s {
	case happyPath:
		return "Happy path"
	case debitRedelivery:
		return "Debit redelivery"
	case creditRejection:
		return "Credit rejection"
	}
	return string(s)
}

func parseScenario(slug string) (scenario, error) {
	if !slices.Contains(scenarios, scenario(slug)) {
		return "", errUnknownScenario
	}
	return scenario(slug), nil
}
