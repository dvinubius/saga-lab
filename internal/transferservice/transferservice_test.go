package transferservice_test

import (
	"bytes"
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
	var logs bytes.Buffer
	s, err := transferservice.Open(context.Background(), db, transferservice.Config{}, slog.New(slog.NewJSONHandler(&logs, nil)))
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
	logged := map[string]any{"level": "INFO", "transfer_id": "unknown", "status": "not found"}
	if got := records(t, &logs, "level", "transfer_id", "status"); !reflect.DeepEqual(got, []map[string]any{logged, logged, logged}) {
		t.Fatalf("logs = %v, want three Info lines for a transfer not found", got)
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

func TestDuplicateEventsAdvanceTheTransferOnce(t *testing.T) {
	type handler func(*transferservice.Service, *message.Message) error
	debited := func(id string) *message.Message {
		return event(t, id, messaging.FundsDebited{TransferID: id, ObservedAt: time.Now()})
	}
	tests := []struct {
		name      string
		before    []handler
		handle    handler
		duplicate func(id string) *message.Message
		want      state
	}{
		{
			name:      "FundsDebited",
			handle:    transferservice.FundsDebited,
			duplicate: debited,
			want:      state{Status: "credit_pending", Steps: []string{"requested", "debit_committed"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}},
		},
		{
			name:   "DebitRejected",
			handle: transferservice.DebitRejected,
			duplicate: func(id string) *message.Message {
				return event(t, id, messaging.DebitRejected{TransferID: id, Reason: "Insufficient funds", ObservedAt: time.Now()})
			},
			want: state{Status: "rejected", Steps: []string{"requested", "debit_rejected"}, Outbox: []string{messaging.DebitFundsTopic}},
		},
		{
			name:   "FundsCredited",
			before: []handler{transferservice.FundsDebited},
			handle: transferservice.FundsCredited,
			duplicate: func(id string) *message.Message {
				return event(t, id, messaging.FundsCredited{TransferID: id, ObservedAt: time.Now()})
			},
			want: state{Status: "completed", Steps: []string{"requested", "debit_committed", "credit_committed", "finished"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := pgtest.NewDatabase(t)
			var logs bytes.Buffer
			s, err := transferservice.Open(context.Background(), db, transferservice.Config{}, slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatalf("open transfer service: %v", err)
			}
			id := submit(t, s)
			for _, before := range test.before {
				if err := before(s, debited(id)); err != nil {
					t.Fatalf("prepare transfer: %v", err)
				}
			}
			msg := test.duplicate(id)

			for range 2 {
				logs.Reset()
				if err := test.handle(s, msg); err != nil {
					t.Fatalf("%s: %v", test.name, err)
				}
			}

			if got := snapshot(t, db, id); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("after duplicate: %+v, want %+v", got, test.want)
			}
			want := []map[string]any{{"level": "INFO", "event": test.name, "transfer_id": id, "message_id": msg.UUID, "status": test.want.Status}}
			if got := records(t, &logs, "level", "event", "transfer_id", "message_id", "status"); !reflect.DeepEqual(got, want) {
				t.Fatalf("logs for duplicate = %v, want %v", got, want)
			}
		})
	}
}

func TestDuplicateObservationIsRecordedOnce(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, transferservice.Config{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	id := submit(t, s)
	observedAt := time.Now().UTC().Truncate(time.Microsecond)
	msg, err := messaging.New(context.Background(), id, messaging.ProcessingObserved{
		TransferID: id, Observation: messaging.DuplicateSuppressed, Service: "Bank A", AttemptID: "attempt-2", ObservedAt: observedAt,
	}, "debit-command")
	if err != nil {
		t.Fatalf("new observation: %v", err)
	}

	for range 2 {
		if err := transferservice.ProcessingObserved(s, msg); err != nil {
			t.Fatalf("ProcessingObserved: %v", err)
		}
	}

	got := transfer(t, s, id)
	if got.Status != "debit_pending" {
		t.Errorf("status = %q, want debit_pending", got.Status)
	}
	want := []historyEntry{
		{Step: "requested", Service: "Transfer Service"},
		{Observation: "DuplicateSuppressed", Service: "Bank A", AttemptID: "attempt-2", MessageID: msg.UUID, CausationID: "debit-command", ObservedAt: observedAt},
	}
	got.History[0] = historyEntry{Step: got.History[0].Step, Service: got.History[0].Service}
	got.History[1].ObservedAt = got.History[1].ObservedAt.UTC()
	if !reflect.DeepEqual(got.History, want) {
		t.Fatalf("history = %+v, want %+v", got.History, want)
	}
}

func TestObservationForUnknownTransferIsIgnored(t *testing.T) {
	db := pgtest.NewDatabase(t)
	var logs bytes.Buffer
	s, err := transferservice.Open(context.Background(), db, transferservice.Config{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	msg := event(t, "unknown", messaging.ProcessingObserved{
		TransferID: "unknown", Observation: messaging.NackRequested, Service: "Bank A", AttemptID: "attempt-1", ObservedAt: time.Now(),
	})

	if err := transferservice.ProcessingObserved(s, msg); err != nil {
		t.Fatalf("ProcessingObserved: %v", err)
	}

	want := []map[string]any{{"level": "INFO", "transfer_id": "unknown", "message_id": msg.UUID}}
	if got := records(t, &logs, "level", "transfer_id", "message_id"); !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
	if rows, err := column(context.Background(), db, `SELECT entry_id::text FROM transfer_history`); err != nil || len(rows) != 0 {
		t.Fatalf("history rows = %v (error %v), want none", rows, err)
	}
}

func TestFundsDebitedCommitsNothingWhenEnqueueFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	s, err := transferservice.Open(ctx, db, transferservice.Config{}, slog.New(slog.NewTextHandler(t.Output(), nil)))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	id := submit(t, s)
	debited := event(t, id, messaging.FundsDebited{TransferID: id, ObservedAt: time.Now()})
	before := snapshot(t, db, id)
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
	if after := snapshot(t, db, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("after failed handling: %+v, want unchanged %+v", after, before)
	}

	if _, err := db.Exec(ctx, `DROP TRIGGER refuse_outbox ON outbox`); err != nil {
		t.Fatalf("drop outbox trigger: %v", err)
	}
	if err := transferservice.FundsDebited(s, debited); err != nil {
		t.Fatalf("FundsDebited: %v", err)
	}
	want := state{Status: "credit_pending", Steps: []string{"requested", "debit_committed"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}}
	if after := snapshot(t, db, id); !reflect.DeepEqual(after, want) {
		t.Fatalf("after handling: %+v, want %+v", after, want)
	}
}

func submit(t *testing.T, s *transferservice.Service) string {
	t.Helper()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount": 25}`)))
	var submitted struct {
		ID string `json:"transfer_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&submitted); err != nil || response.Code != http.StatusAccepted {
		t.Fatalf("POST /api/transfers: status %d, decode error %v", response.Code, err)
	}
	return submitted.ID
}

type historyEntry struct {
	Step        string    `json:"step"`
	Observation string    `json:"observation"`
	Service     string    `json:"service"`
	AttemptID   string    `json:"attempt_id"`
	MessageID   string    `json:"message_id"`
	CausationID string    `json:"causation_id"`
	ObservedAt  time.Time `json:"observed_at"`
}

type transferJSON struct {
	Status  string         `json:"status"`
	History []historyEntry `json:"history"`
}

func transfer(t *testing.T, s *transferservice.Service, id string) transferJSON {
	t.Helper()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/transfers/"+id, nil))
	var got transferJSON
	if err := json.NewDecoder(response.Body).Decode(&got); err != nil || response.Code != http.StatusOK {
		t.Fatalf("GET /api/transfers/%s: status %d, decode error %v", id, response.Code, err)
	}
	return got
}

func records(t *testing.T, logs *bytes.Buffer, keys ...string) []map[string]any {
	t.Helper()
	var got []map[string]any
	decoder := json.NewDecoder(logs)
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log record: %v", err)
		}
		picked := map[string]any{}
		for _, key := range keys {
			if value, ok := record[key]; ok {
				picked[key] = value
			}
		}
		got = append(got, picked)
	}
	return got
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
