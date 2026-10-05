package transferservice_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"io"
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
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)))
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
	s.Handler().ServeHTTP(response, testRequest(http.MethodGet, "/api/transfers", nil))
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
	creditRejected := func(id string) *message.Message {
		return event(t, id, messaging.CreditRejected{TransferID: id, Reason: "Credit refused by Bank B", ObservedAt: time.Now()})
	}
	type prior struct {
		handle handler
		event  func(id string) *message.Message
	}
	tests := []struct {
		name      string
		before    []prior
		handle    handler
		duplicate func(id string) *message.Message
		want      state
	}{
		{
			name:      "FundsDebited",
			handle:    transferservice.FundsDebited,
			duplicate: debited,
			want:      state{Status: "credit_pending", Steps: []string{"requested", "debit_committed", "credit_requested"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}},
		},
		{
			name:   "DebitRejected",
			handle: transferservice.DebitRejected,
			duplicate: func(id string) *message.Message {
				return event(t, id, messaging.DebitRejected{TransferID: id, Reason: "Insufficient funds", ObservedAt: time.Now()})
			},
			want: state{Status: "rejected", Steps: []string{"requested", "debit_rejected", "transfer_rejected"}, Outbox: []string{messaging.DebitFundsTopic}},
		},
		{
			name:   "FundsCredited",
			before: []prior{{transferservice.FundsDebited, debited}},
			handle: transferservice.FundsCredited,
			duplicate: func(id string) *message.Message {
				return event(t, id, messaging.FundsCredited{TransferID: id, ObservedAt: time.Now()})
			},
			want: state{Status: "completed", Steps: []string{"requested", "debit_committed", "credit_requested", "credit_committed", "finished"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}},
		},
		{
			name:      "CreditRejected",
			before:    []prior{{transferservice.FundsDebited, debited}},
			handle:    transferservice.CreditRejected,
			duplicate: creditRejected,
			want:      state{Status: "refund_pending", Steps: []string{"requested", "debit_committed", "credit_requested", "credit_rejected", "refund_requested"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic, messaging.RefundFundsTopic}},
		},
		{
			name:   "FundsRefunded",
			before: []prior{{transferservice.FundsDebited, debited}, {transferservice.CreditRejected, creditRejected}},
			handle: transferservice.FundsRefunded,
			duplicate: func(id string) *message.Message {
				return event(t, id, messaging.FundsRefunded{TransferID: id, ObservedAt: time.Now()})
			},
			want: state{Status: "refunded", Steps: []string{"requested", "debit_committed", "credit_requested", "credit_rejected", "refund_requested", "refund_committed", "transfer_refunded"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic, messaging.RefundFundsTopic}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := pgtest.NewDatabase(t)
			var logs bytes.Buffer
			s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatalf("open transfer service: %v", err)
			}
			id := submit(t, s)
			for _, before := range test.before {
				if err := before.handle(s, before.event(id)); err != nil {
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
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.DiscardHandler))
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

func TestHistoryIsOrderedByObservationTime(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	id := submit(t, s)
	debitedAt := time.Now()
	if err := transferservice.FundsDebited(s, event(t, id, messaging.FundsDebited{TransferID: id, ObservedAt: debitedAt})); err != nil {
		t.Fatalf("FundsDebited: %v", err)
	}
	nack := event(t, id, messaging.ProcessingObserved{
		TransferID: id, Observation: messaging.NackRequested, Service: "Bank A", AttemptID: "attempt-1", ObservedAt: debitedAt,
	})
	if err := transferservice.ProcessingObserved(s, nack); err != nil {
		t.Fatalf("ProcessingObserved: %v", err)
	}

	var got []string
	for _, e := range transfer(t, s, id).History {
		got = append(got, e.Step+e.Observation)
	}
	if want := []string{"requested", "debit_committed", "NackRequested", "credit_requested"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
}

func TestObservationForUnknownTransferIsIgnored(t *testing.T) {
	db := pgtest.NewDatabase(t)
	var logs bytes.Buffer
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)))
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
	s, err := transferservice.Open(ctx, db, bankConfig(t), slog.New(slog.NewTextHandler(t.Output(), nil)))
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
	want := state{Status: "credit_pending", Steps: []string{"requested", "debit_committed", "credit_requested"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic}}
	if after := snapshot(t, db, id); !reflect.DeepEqual(after, want) {
		t.Fatalf("after handling: %+v, want %+v", after, want)
	}
}

func TestCreditRejectedCommitsNothingWhenEnqueueFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	s, err := transferservice.Open(ctx, db, bankConfig(t), slog.New(slog.NewTextHandler(t.Output(), nil)))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	id := submit(t, s)
	if err := transferservice.FundsDebited(s, event(t, id, messaging.FundsDebited{TransferID: id, ObservedAt: time.Now()})); err != nil {
		t.Fatalf("FundsDebited: %v", err)
	}
	rejectedCredit := event(t, id, messaging.CreditRejected{TransferID: id, Reason: "Credit refused by Bank B", ObservedAt: time.Now()})
	before := snapshot(t, db, id)

	if _, err := db.Exec(ctx, `
		CREATE FUNCTION refuse_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox refused'; END $$;
		CREATE TRIGGER refuse_outbox BEFORE INSERT ON outbox FOR EACH ROW EXECUTE FUNCTION refuse_outbox();`); err != nil {
		t.Fatalf("install outbox trigger: %v", err)
	}
	if err := transferservice.CreditRejected(s, rejectedCredit); err == nil {
		t.Fatal("CreditRejected with a refusing outbox succeeded, want an error")
	}
	if after := snapshot(t, db, id); !reflect.DeepEqual(after, before) {
		t.Fatalf("after failed handling: %+v, want unchanged %+v", after, before)
	}
	if got := transfer(t, s, id).RejectionReason; got != "" {
		t.Fatalf("rejection reason after failed handling = %q, want none", got)
	}

	if _, err := db.Exec(ctx, `DROP TRIGGER refuse_outbox ON outbox`); err != nil {
		t.Fatalf("drop outbox trigger: %v", err)
	}
	for range 2 {
		if err := transferservice.CreditRejected(s, rejectedCredit); err != nil {
			t.Fatalf("CreditRejected: %v", err)
		}
	}
	want := state{Status: "refund_pending", Steps: []string{"requested", "debit_committed", "credit_requested", "credit_rejected", "refund_requested"}, Outbox: []string{messaging.DebitFundsTopic, messaging.CreditFundsTopic, messaging.RefundFundsTopic}}
	if after := snapshot(t, db, id); !reflect.DeepEqual(after, want) {
		t.Fatalf("after handling: %+v, want %+v", after, want)
	}
	if got := transfer(t, s, id).RejectionReason; got != "Credit refused by Bank B" {
		t.Fatalf("rejection reason = %q, want Bank B's", got)
	}
}

func TestRefundPendingTransferHoldsSubmission(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}
	id := submit(t, s)
	if err := transferservice.FundsDebited(s, event(t, id, messaging.FundsDebited{TransferID: id, ObservedAt: time.Now()})); err != nil {
		t.Fatalf("FundsDebited: %v", err)
	}
	if err := transferservice.CreditRejected(s, event(t, id, messaging.CreditRejected{TransferID: id, Reason: "Credit refused by Bank B", ObservedAt: time.Now()})); err != nil {
		t.Fatalf("CreditRejected: %v", err)
	}

	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, testRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount": 10}`)))
	var problem struct {
		PendingTransferID string `json:"pending_transfer_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil || response.Code != http.StatusConflict {
		t.Fatalf("POST /api/transfers while refund pending: status %d, decode error %v, want %d", response.Code, err, http.StatusConflict)
	}
	if problem.PendingTransferID != id {
		t.Fatalf("conflict names pending transfer %q, want %q", problem.PendingTransferID, id)
	}
}

func submit(t *testing.T, s *transferservice.Service) string {
	t.Helper()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, testRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount": 25}`)))
	var submitted struct {
		ID string `json:"transfer_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&submitted); err != nil || response.Code != http.StatusAccepted {
		t.Fatalf("POST /api/transfers: status %d, decode error %v", response.Code, err)
	}
	return submitted.ID
}

type historyEntry struct {
	IssuedMessageID string    `json:"issued_message_id"`
	Step            string    `json:"step"`
	Observation     string    `json:"observation"`
	Service         string    `json:"service"`
	AttemptID       string    `json:"attempt_id"`
	MessageID       string    `json:"message_id"`
	CausationID     string    `json:"causation_id"`
	ObservedAt      time.Time `json:"observed_at"`
}

type transferJSON struct {
	ID                 string         `json:"transfer_id"`
	VisualisationReady bool           `json:"visualisation_ready"`
	Status             string         `json:"status"`
	RejectionReason    string         `json:"rejection_reason"`
	History            []historyEntry `json:"history"`
}

func transfer(t *testing.T, s *transferservice.Service, id string) transferJSON {
	t.Helper()
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, testRequest(http.MethodGet, "/api/transfers/"+id, nil))
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

func bankConfig(t *testing.T) transferservice.Config {
	t.Helper()
	banks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || !strings.HasPrefix(r.URL.Path, "/accounts/") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(banks.Close)
	return transferservice.Config{ResumeWait: 2500 * time.Millisecond, BankAURL: banks.URL, BankBURL: banks.URL}
}

func testRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.AddCookie(&http.Cookie{Name: visitor.CookieName, Value: "test-visitor"})
	return r
}

func TestNewVisitorRetriesUnavailableAccountOpening(t *testing.T) {
	for _, path := range []string{"/api/transfers", "/"} {
		t.Run(path, func(t *testing.T) {
			db := pgtest.NewDatabase(t)
			available := false
			banks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !available {
					http.Error(w, "bank unavailable", http.StatusServiceUnavailable)
					return
				}
				if r.Method == http.MethodPut {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"balance":100}`)
			}))
			defer banks.Close()
			config := bankConfig(t)
			config.BankBURL = banks.URL
			s, err := transferservice.Open(context.Background(), db, config, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			failed := httptest.NewRecorder()
			s.Handler().ServeHTTP(failed, httptest.NewRequest(http.MethodGet, path, nil))
			if failed.Code != http.StatusBadGateway {
				t.Fatalf("unavailable opening: status %d, body %q", failed.Code, failed.Body)
			}
			cookies := failed.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("visitor cookies = %v, want one", cookies)
			}
			cookie := cookies[0]
			id, err := hex.DecodeString(cookie.Value)
			if err != nil || len(id) != 32 {
				t.Fatalf("visitor ID %q is not an opaque 32-byte random ID", cookie.Value)
			}
			if cookie.Name != visitor.CookieName || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.MaxAge < 30*24*60*60 {
				t.Fatalf("visitor cookie attributes = %+v", cookie)
			}
			available = true
			retry := httptest.NewRequest(http.MethodGet, "/api/transfers", nil)
			retry.AddCookie(cookie)
			succeeded := httptest.NewRecorder()
			s.Handler().ServeHTTP(succeeded, retry)
			if succeeded.Code != http.StatusOK {
				t.Fatalf("retry opening: status %d, body %q", succeeded.Code, succeeded.Body)
			}
			available = false
			returning := httptest.NewRecorder()
			s.Handler().ServeHTTP(returning, retry)
			if returning.Code != http.StatusOK {
				t.Fatalf("known visitor needs no reopening: status %d, body %q", returning.Code, returning.Body)
			}
		})
	}
}
