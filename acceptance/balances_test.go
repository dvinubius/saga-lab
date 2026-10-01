package acceptance_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
)

const appURLVariable = "SAGA_LAB_URL"

func TestPreparedVisitorSeesInitialBalances(t *testing.T) {
	var balances struct {
		BankA struct {
			Balance *int64 `json:"balance"`
		} `json:"bank_a"`
		BankB struct {
			Balance *int64 `json:"balance"`
		} `json:"bank_b"`
	}

	body := get(t, "/api/balances")
	if err := json.Unmarshal(body, &balances); err != nil {
		t.Fatalf("decode balances %q: %v", body, err)
	}

	assertBalance(t, "Bank A", balances.BankA.Balance, 100)
	assertBalance(t, "Bank B", balances.BankB.Balance, 0)
}

func TestPageShowsPreparedBalances(t *testing.T) {
	page := get(t, "/")

	assertBalance(t, "Bank A", pageBalance(t, page, "bank-a-balance"), 100)
	assertBalance(t, "Bank B", pageBalance(t, page, "bank-b-balance"), 0)
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

func get(t *testing.T, path string) []byte {
	t.Helper()
	baseURL := os.Getenv(appURLVariable)
	if baseURL == "" {
		t.Skipf("%s is not set; run scripts/test.sh", appURLVariable)
	}
	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %q", path, response.StatusCode, body)
	}
	return body
}
