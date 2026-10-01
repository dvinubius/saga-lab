package acceptance_test

import (
	"encoding/json"
	"regexp"
	"strconv"
	"testing"
)

func TestPreparedVisitorSeesInitialBalances(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	t.Run("API", func(t *testing.T) {
		demo.assertBalances(t, 100, 0)
	})
	t.Run("page", func(t *testing.T) {
		page := demo.get(t, "/")
		assertBalance(t, "Bank A", pageBalance(t, page, "bank-a-balance"), 100)
		assertBalance(t, "Bank B", pageBalance(t, page, "bank-b-balance"), 0)
	})
}

func (d *demonstration) assertBalances(t *testing.T, bankA, bankB int64) {
	t.Helper()
	var balances struct {
		BankA struct {
			Balance *int64 `json:"balance"`
		} `json:"bank_a"`
		BankB struct {
			Balance *int64 `json:"balance"`
		} `json:"bank_b"`
	}
	body := d.get(t, "/api/balances")
	if err := json.Unmarshal(body, &balances); err != nil {
		t.Fatalf("decode balances %q: %v", body, err)
	}
	assertBalance(t, "Bank A", balances.BankA.Balance, bankA)
	assertBalance(t, "Bank B", balances.BankB.Balance, bankB)
}

func pageBalance(t *testing.T, page []byte, id string) *int64 {
	t.Helper()
	match := regexp.MustCompile(`id="` + id + `"[^>]*value="(-?\d+)"`).FindSubmatch(page)
	if match == nil {
		return nil
	}
	balance, err := strconv.ParseInt(string(match[1]), 10, 64)
	if err != nil {
		t.Fatalf("parse %s: %v", id, err)
	}
	return &balance
}

func assertBalance(t *testing.T, bank string, got *int64, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s balance missing, want %d", bank, want)
		return
	}
	if *got != want {
		t.Errorf("%s balance = %d, want %d", bank, *got, want)
	}
}
