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
)

var scenarios = []scenario{happyPath, debitRedelivery}

var errUnknownScenario = errors.New("scenario must be one of: happy_path, debit_redelivery")

func (s scenario) Label() string {
	switch s {
	case happyPath:
		return "Happy path"
	case debitRedelivery:
		return "Debit redelivery"
	}
	return string(s)
}

func parseScenario(slug string) (scenario, error) {
	if !slices.Contains(scenarios, scenario(slug)) {
		return "", errUnknownScenario
	}
	return scenario(slug), nil
}
