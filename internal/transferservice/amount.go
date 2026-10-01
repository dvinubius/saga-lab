package transferservice

import (
	"errors"
	"strconv"
	"strings"
)

var (
	errAmountNotWhole    = errors.New("amount must be a whole number of credits, written with digits only")
	errAmountNotPositive = errors.New("amount must be greater than zero")
	errAmountTooLarge    = errors.New("amount is too large")
)

func parseAmount(text string) (int64, error) {
	if digits, negative := strings.CutPrefix(text, "-"); negative && onlyDigits(digits) {
		return 0, errAmountNotPositive
	}
	if !onlyDigits(text) {
		return 0, errAmountNotWhole
	}
	amount, err := strconv.ParseInt(text, 10, 64)
	if errors.Is(err, strconv.ErrRange) {
		return 0, errAmountTooLarge
	}
	if err != nil {
		return 0, errAmountNotWhole
	}
	if amount == 0 {
		return 0, errAmountNotPositive
	}
	return amount, nil
}

func onlyDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
