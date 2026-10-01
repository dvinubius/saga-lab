package bank_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPreparedAccountStartsWithConfiguredBalance(t *testing.T) {
	db := pgtest.NewDatabase(t)

	b := open(t, db, bank.Config{PreparedBalance: 100})

	if got := balance(t, b, visitor.PreparedID); got != 100 {
		t.Fatalf("prepared balance = %d, want 100", got)
	}
}

func TestLaterStartupPreservesExistingAccount(t *testing.T) {
	db := pgtest.NewDatabase(t)
	open(t, db, bank.Config{PreparedBalance: 100})

	restarted := open(t, db, bank.Config{PreparedBalance: 7})

	if got := balance(t, restarted, visitor.PreparedID); got != 100 {
		t.Fatalf("prepared balance after restart = %d, want existing 100", got)
	}
}

func TestVisitorWithoutAccountIsNotFound(t *testing.T) {
	db := pgtest.NewDatabase(t)
	b := open(t, db, bank.Config{PreparedBalance: 0})

	if got := get(b, "/accounts/unknown-visitor").Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", got, http.StatusNotFound)
	}
}

func open(t *testing.T, db *pgxpool.Pool, config bank.Config) *bank.Bank {
	t.Helper()
	b, err := bank.Open(context.Background(), db, config)
	if err != nil {
		t.Fatalf("open bank: %v", err)
	}
	return b
}

func balance(t *testing.T, b *bank.Bank, visitorID string) int64 {
	t.Helper()
	response := get(b, "/accounts/"+visitorID)
	if response.Code != http.StatusOK {
		t.Fatalf("GET account: status %d, body %q", response.Code, response.Body)
	}
	var account struct {
		Balance int64 `json:"balance"`
	}
	if err := json.NewDecoder(response.Body).Decode(&account); err != nil {
		t.Fatalf("decode account: %v", err)
	}
	return account.Balance
}

func get(b *bank.Bank, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	b.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}
