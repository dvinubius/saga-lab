package bank_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/jackc/pgx/v5"
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

func TestDebitCommitsNothingWhenEnqueueFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	b := open(t, db, bank.Config{PreparedBalance: 100, Role: bank.Source})
	command, err := messaging.New(ctx, "transfer", messaging.DebitFunds{TransferID: "transfer", VisitorID: visitor.PreparedID, Amount: 25}, "")
	if err != nil {
		t.Fatalf("new command: %v", err)
	}

	if _, err := db.Exec(ctx, `
		CREATE FUNCTION refuse_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox refused'; END $$;
		CREATE TRIGGER refuse_outbox BEFORE INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION refuse_outbox();`); err != nil {
		t.Fatalf("install outbox trigger: %v", err)
	}
	if err := bank.DebitFunds(b, command); err == nil {
		t.Fatal("DebitFunds with a refusing outbox succeeded, want an error")
	}
	if got := balance(t, b, visitor.PreparedID); got != 100 {
		t.Fatalf("balance after failed handling = %d, want unchanged 100", got)
	}
	if got := outbox(t, db); len(got) != 0 {
		t.Fatalf("outbox after failed handling = %v, want empty", got)
	}
	if got := inbox(t, db); got != 0 {
		t.Fatalf("inbox rows after failed handling = %d, want none", got)
	}

	if _, err := db.Exec(ctx, `DROP TRIGGER refuse_outbox ON outbox`); err != nil {
		t.Fatalf("drop outbox trigger: %v", err)
	}
	if err := bank.DebitFunds(b, command); err != nil {
		t.Fatalf("DebitFunds: %v", err)
	}
	if got := balance(t, b, visitor.PreparedID); got != 75 {
		t.Fatalf("balance after handling = %d, want 75", got)
	}
	if got, want := outbox(t, db), []string{messaging.FundsDebitedTopic}; !reflect.DeepEqual(got, want) {
		t.Fatalf("outbox after handling = %v, want %v", got, want)
	}
}

func TestDuplicateDebitAppliesNothing(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	b := open(t, db, bank.Config{PreparedBalance: 100, Role: bank.Source})
	command, err := messaging.New(ctx, "transfer", messaging.DebitFunds{TransferID: "transfer", VisitorID: visitor.PreparedID, Amount: 25}, "")
	if err != nil {
		t.Fatalf("new command: %v", err)
	}

	for range 2 {
		if err := bank.DebitFunds(b, command); err != nil {
			t.Fatalf("DebitFunds: %v", err)
		}
	}

	if got := balance(t, b, visitor.PreparedID); got != 75 {
		t.Errorf("balance after duplicate debit = %d, want 75", got)
	}
	if got, want := outbox(t, db), []string{messaging.FundsDebitedTopic, messaging.ProcessingObservedTopic}; !reflect.DeepEqual(got, want) {
		t.Errorf("outbox after duplicate debit = %v, want %v", got, want)
	}
	assertObservations(t, db, messaging.DuplicateSuppressed)
}

func TestDuplicateCreditAppliesNothing(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	b := open(t, db, bank.Config{PreparedBalance: 100, Role: bank.Destination})
	command, err := messaging.New(ctx, "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: visitor.PreparedID, Amount: 25}, "")
	if err != nil {
		t.Fatalf("new command: %v", err)
	}

	for range 2 {
		if err := bank.CreditFunds(b, command); err != nil {
			t.Fatalf("CreditFunds: %v", err)
		}
	}

	if got := balance(t, b, visitor.PreparedID); got != 125 {
		t.Errorf("balance after duplicate credit = %d, want 125", got)
	}
	if got, want := outbox(t, db), []string{messaging.FundsCreditedTopic, messaging.ProcessingObservedTopic}; !reflect.DeepEqual(got, want) {
		t.Errorf("outbox after duplicate credit = %v, want %v", got, want)
	}
	assertObservations(t, db, messaging.DuplicateSuppressed)
}

func TestDuplicateCreditRejectionAppliesNothing(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	b := open(t, db, bank.Config{PreparedBalance: 0, Role: bank.Destination})
	command, err := messaging.New(ctx, "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: visitor.PreparedID, Amount: 25, Scenario: messaging.CreditRejection}, "")
	if err != nil {
		t.Fatalf("new command: %v", err)
	}

	for range 2 {
		if err := bank.CreditFunds(b, command); err != nil {
			t.Fatalf("CreditFunds: %v", err)
		}
	}

	if got := balance(t, b, visitor.PreparedID); got != 0 {
		t.Errorf("balance after rejected credit = %d, want unchanged 0", got)
	}
	if got, want := outbox(t, db), []string{messaging.CreditRejectedTopic, messaging.ProcessingObservedTopic}; !reflect.DeepEqual(got, want) {
		t.Errorf("outbox after duplicate rejected credit = %v, want %v", got, want)
	}
	assertObservations(t, db, messaging.DuplicateSuppressed)
}

func assertObservations(t *testing.T, db *pgxpool.Pool, want ...string) {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT convert_from(payload, 'UTF8')::jsonb->>'observation' FROM outbox WHERE topic = $1 ORDER BY id`, messaging.ProcessingObservedTopic)
	if err != nil {
		t.Fatalf("read observations: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read observations: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("enqueued observations = %q, want %q", got, want)
	}
}

func inbox(t *testing.T, db *pgxpool.Pool) int {
	t.Helper()
	var rows int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM inbox`).Scan(&rows); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	return rows
}

func outbox(t *testing.T, db *pgxpool.Pool) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), `SELECT topic FROM outbox ORDER BY id`)
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	topics, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return topics
}

func open(t *testing.T, db *pgxpool.Pool, config bank.Config) *bank.Bank {
	t.Helper()
	b, err := bank.Open(context.Background(), db, config, slog.New(slog.NewTextHandler(t.Output(), nil)))
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
