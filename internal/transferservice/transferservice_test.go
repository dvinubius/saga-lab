package transferservice_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/transferservice"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEventsForUnknownTransferAreIgnoredWithoutBroker(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, transferservice.Config{}, slog.New(slog.NewTextHandler(t.Output(), nil)))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}

	if err := transferservice.FundsDebited(s, event(t, "unknown", messaging.FundsDebited{TransferID: "unknown", ObservedAt: time.Now()})); err != nil {
		t.Fatalf("FundsDebited: %v", err)
	}
	if err := transferservice.DebitRejected(s, event(t, "unknown", messaging.DebitRejected{TransferID: "unknown", Reason: "Insufficient funds", ObservedAt: time.Now()})); err != nil {
		t.Fatalf("DebitRejected: %v", err)
	}
	if err := transferservice.FundsCredited(s, event(t, "unknown", messaging.FundsCredited{TransferID: "unknown", ObservedAt: time.Now()})); err != nil {
		t.Fatalf("FundsCredited: %v", err)
	}

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/transfers", nil))
	var body struct {
		Transfers []json.RawMessage `json:"transfers"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil || response.Code != http.StatusOK {
		t.Fatalf("GET /api/transfers: status %d, decode error %v", response.Code, err)
	}
	if len(body.Transfers) != 0 {
		t.Fatalf("transfers = %d, want none", len(body.Transfers))
	}
}

func TestFundsDebitedCommitsNothingWhenEnqueueFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	s, err := transferservice.Open(ctx, db, transferservice.Config{}, slog.New(slog.NewTextHandler(t.Output(), nil)))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount": 25}`)))
	var submitted struct {
		ID string `json:"transfer_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&submitted); err != nil || response.Code != http.StatusAccepted {
		t.Fatalf("POST /api/transfers: status %d, decode error %v", response.Code, err)
	}
	debited := event(t, submitted.ID, messaging.FundsDebited{TransferID: submitted.ID, ObservedAt: time.Now()})
	before := snapshot(t, db, submitted.ID)
	if want := (state{Status: "debit_pending", Steps: []string{"requested"}, Outbox: []string{messaging.DebitFundsTopic}}); !reflect.DeepEqual(before, want) {
		t.Fatalf("after submission: %+v, want %+v", before, want)
	}

	if _, err := db.Exec(ctx, `
		CREATE FUNCTION refuse_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox refused'; END $$;
		CREATE TRIGGER refuse_outbox BEFORE INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION refuse_outbox();`); err != nil {
		t.Fatalf("install outbox trigger: %v", err)
	}
	if err := transferservice.FundsDebited(s, debited); err == nil {
		t.Fatal("FundsDebited with a refusing outbox succeeded, want an error")
	}
	if after := snapshot(t, db, submitted.ID); !reflect.DeepEqual(after, before) {
		t.Fatalf("after failed handling: %+v, want unchanged %+v", after, before)
	}

	if _, err := db.Exec(ctx, `DROP TRIGGER refuse_outbox ON outbox`); err != nil {
		t.Fatalf("drop outbox trigger: %v", err)
	}
	if err := transferservice.FundsDebited(s, debited); err != nil {
		t.Fatalf("FundsDebited: %v", err)
	}
	want := state{Status: "credit_pending", Steps: []string{"requested", "debit_committed"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}}
	if after := snapshot(t, db, submitted.ID); !reflect.DeepEqual(after, want) {
		t.Fatalf("after handling: %+v, want %+v", after, want)
	}
}

type state struct {
	Status string
	Steps  []string
	Outbox []string
}

func snapshot(t *testing.T, db *pgxpool.Pool, transferID string) state {
	t.Helper()
	ctx := context.Background()
	var s state
	if err := db.QueryRow(ctx, `SELECT status FROM transfers WHERE transfer_id = $1`, transferID).Scan(&s.Status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	var err error
	if s.Steps, err = column(ctx, db, `SELECT step FROM transfer_history WHERE transfer_id = $1 ORDER BY entry_id`, transferID); err != nil {
		t.Fatalf("read history: %v", err)
	}
	if s.Outbox, err = column(ctx, db, `SELECT topic FROM outbox ORDER BY id`); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	return s
}

func column(ctx context.Context, db *pgxpool.Pool, query string, args ...any) ([]string, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func event(t *testing.T, transferID string, payload any) *message.Message {
	t.Helper()
	msg, err := messaging.New(context.Background(), transferID, payload, "")
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	return msg
}
