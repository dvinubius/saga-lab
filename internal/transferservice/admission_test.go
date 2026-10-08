package transferservice_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/transferservice"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func admissionRequest(s *transferservice.Service, who, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(&http.Cookie{Name: visitor.CookieName, Value: who})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func submitUnavailable(t *testing.T, s *transferservice.Service, who string) transferJSON {
	t.Helper()
	w := admissionRequest(s, who, http.MethodPost, "/api/transfers", `{"amount":25,"scenario":"bank_b_unavailable"}`)
	var got transferJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s, %v", w.Code, w.Body, err)
	}
	return got
}

func admissionTransfer(t *testing.T, s *transferservice.Service, who, id string) transferJSON {
	t.Helper()
	w := admissionRequest(s, who, http.MethodGet, "/api/transfers/"+id, "")
	var got transferJSON
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK {
		t.Fatalf("transfer: %d %s, %v", w.Code, w.Body, err)
	}
	return got
}

func TestConcurrentUnavailableSubmissionsStartOneAndQueueTheRest(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	const count = 8
	responses := make([]*httptest.ResponseRecorder, count)
	start := make(chan struct{})
	var group sync.WaitGroup
	for i := range count {
		group.Go(func() {
			<-start
			responses[i] = admissionRequest(s, fmt.Sprintf("visitor-%d", i), http.MethodPost, "/api/transfers", `{"amount":25,"scenario":"bank_b_unavailable"}`)
		})
	}
	close(start)
	group.Wait()
	running, waiting := 0, 0
	for _, w := range responses {
		var got transferJSON
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusAccepted {
			t.Fatalf("submit: %d %s, %v", w.Code, w.Body, err)
		}
		switch got.Status {
		case "debit_pending":
			running++
		case "awaiting_admission":
			waiting++
			if got.VisualisationReady || len(got.History) != 1 {
				t.Fatalf("waiting = %+v", got)
			}
		default:
			t.Fatalf("unexpected status %q", got.Status)
		}
	}
	if running != 1 || waiting != count-1 {
		t.Fatalf("running %d, waiting %d; want 1, %d", running, waiting, count-1)
	}
	topics, err := column(context.Background(), db, `SELECT topic FROM outbox`)
	if err != nil || len(topics) != 1 || topics[0] != "DebitFunds" {
		t.Fatalf("commands = %v, %v; want one DebitFunds", topics, err)
	}
}

func TestReleaseAdmitsOldestWaitingTransferAfterBothCompletionAndPause(t *testing.T) {
	for _, order := range []string{"completion first", "pause first", "rejection"} {
		t.Run(order, func(t *testing.T) {
			db := pgtest.NewDatabase(t)
			var logs bytes.Buffer
			s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.NewJSONHandler(&logs, nil)))
			if err != nil {
				t.Fatal(err)
			}
			holder := submitUnavailable(t, s, "holder")
			first := submitUnavailable(t, s, "first")
			last := submitUnavailable(t, s, "last")
			if holder.History[0].IssuedMessageID == "" || first.History[0].IssuedMessageID != "" {
				t.Fatalf("requested commands: holder %+v, first %+v", holder.History, first.History)
			}
			pause := event(t, holder.ID, messaging.ProcessingObserved{TransferID: holder.ID, Observation: messaging.DeliveryPaused, Service: messaging.BankB, ObservedAt: time.Now()})
			complete := event(t, holder.ID, messaging.FundsCredited{TransferID: holder.ID, ObservedAt: time.Now()})
			reject := event(t, holder.ID, messaging.DebitRejected{TransferID: holder.ID, Reason: "Insufficient funds", ObservedAt: time.Now()})
			if order == "rejection" {
				if err := transferservice.DebitRejected(s, reject); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := transferservice.FundsDebited(s, event(t, holder.ID, messaging.FundsDebited{TransferID: holder.ID, ObservedAt: time.Now()})); err != nil {
					t.Fatal(err)
				}
				if order == "completion first" {
					err = transferservice.FundsCredited(s, complete)
				} else {
					err = transferservice.ProcessingObserved(s, pause)
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := admissionTransfer(t, s, "first", first.ID); got.Status != "awaiting_admission" {
					t.Fatalf("released before both halves: %+v", got)
				}
				if order == "completion first" {
					err = transferservice.ProcessingObserved(s, pause)
				} else {
					err = transferservice.FundsCredited(s, complete)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			admitted := admissionTransfer(t, s, "first", first.ID)
			if admitted.Status != "debit_pending" || len(admitted.History) != 2 || admitted.History[1].Observation != "Admitted" || admitted.History[1].IssuedMessageID == "" || admitted.History[1].Service != "Transfer Service" {
				t.Fatalf("admission = %+v", admitted)
			}
			var command messaging.DebitFunds
			var payload []byte
			if err := db.QueryRow(context.Background(), `SELECT payload FROM outbox WHERE message_id = $1 AND topic = 'DebitFunds'`, admitted.History[1].IssuedMessageID).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(payload, &command); err != nil || command.TransferID != first.ID || command.VisitorID != "first" || command.Amount != 25 {
				t.Fatalf("debit = %+v, %v", command, err)
			}
			if order == "rejection" {
				err = transferservice.DebitRejected(s, reject)
			} else {
				if err = transferservice.FundsCredited(s, complete); err == nil {
					err = transferservice.ProcessingObserved(s, pause)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := admissionTransfer(t, s, "last", last.ID); got.Status != "awaiting_admission" {
				t.Fatalf("stale holder released next slot: %+v", got)
			}
			if got := admissionTransfer(t, s, "holder", holder.ID); slices.ContainsFunc(got.History, func(e historyEntry) bool { return e.Observation == "Admitted" }) {
				t.Fatalf("uncontended transfer admitted: %+v", got)
			}
			logRecords := records(t, &logs, "level", "msg", "transfer_id")
			for _, want := range []map[string]any{
				{"level": "INFO", "msg": "transfer queued for admission", "transfer_id": first.ID},
				{"level": "INFO", "msg": "transfer admitted", "transfer_id": first.ID},
				{"level": "INFO", "msg": "demonstration slot released", "transfer_id": holder.ID},
			} {
				if !slices.ContainsFunc(logRecords, func(got map[string]any) bool { return reflect.DeepEqual(got, want) }) {
					t.Errorf("missing log %v in %v", want, logRecords)
				}
			}
		})
	}
}

func TestWaitingTransferRetainsThePendingRestrictionAfterMigration(t *testing.T) {
	db := pgtest.NewDatabase(t)
	config := bankConfig(t)
	s, err := transferservice.Open(context.Background(), db, config, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(context.Background(), `DROP INDEX pending_transfer_restriction; CREATE UNIQUE INDEX pending_transfer_restriction ON transfers (visitor_id) WHERE status IN ('debit_pending', 'credit_pending', 'refund_pending')`); err != nil {
		t.Fatal(err)
	}
	s, err = transferservice.Open(context.Background(), db, config, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	submitUnavailable(t, s, "holder")
	waiting := submitUnavailable(t, s, "waiting")
	if waiting.Status != "awaiting_admission" {
		t.Fatalf("waiting = %+v", waiting)
	}
	for _, scenario := range []string{"bank_b_unavailable", "happy_path"} {
		w := admissionRequest(s, "waiting", http.MethodPost, "/api/transfers", `{"amount":10,"scenario":"`+scenario+`"}`)
		var conflict struct {
			ID string `json:"pending_transfer_id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &conflict); err != nil || w.Code != http.StatusConflict || conflict.ID != waiting.ID {
			t.Fatalf("pending restriction: %d %s, %v", w.Code, w.Body, err)
		}
	}
	w := admissionRequest(s, "waiting", http.MethodGet, "/api/transfers", "")
	var list struct {
		Transfers []transferJSON `json:"transfers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Transfers) != 1 {
		t.Fatalf("waiting visitor list = %s, %v", w.Body, err)
	}
}

func TestHolderResubmissionAndCompletionReleaseWithoutDeadlock(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	holder := submitUnavailable(t, s, "holder")
	waiting := submitUnavailable(t, s, "waiting")
	if err := transferservice.FundsDebited(s, event(t, holder.ID, messaging.FundsDebited{TransferID: holder.ID, ObservedAt: time.Now()})); err != nil {
		t.Fatal(err)
	}
	if err := transferservice.ProcessingObserved(s, event(t, holder.ID, messaging.ProcessingObserved{TransferID: holder.ID, Observation: messaging.DeliveryPaused, Service: messaging.BankB, ObservedAt: time.Now()})); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	completion := event(t, holder.ID, messaging.FundsCredited{TransferID: holder.ID, ObservedAt: time.Now()})
	completion.SetContext(ctx)
	responses := make([]*httptest.ResponseRecorder, 12)
	var completionError error
	var group sync.WaitGroup
	start := make(chan struct{})
	group.Go(func() { <-start; completionError = transferservice.FundsCredited(s, completion) })
	for i := range responses {
		group.Go(func() {
			<-start
			r := httptest.NewRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount":25,"scenario":"bank_b_unavailable"}`)).WithContext(ctx)
			r.AddCookie(&http.Cookie{Name: visitor.CookieName, Value: "holder"})
			responses[i] = httptest.NewRecorder()
			s.Handler().ServeHTTP(responses[i], r)
		})
	}
	close(start)
	group.Wait()
	if completionError != nil {
		t.Fatalf("completion failed: %v", completionError)
	}
	for _, response := range responses {
		if response.Code != http.StatusAccepted && response.Code != http.StatusConflict {
			t.Fatalf("resubmission failed: %d %s", response.Code, response.Body)
		}
	}
	if got := admissionTransfer(t, s, "waiting", waiting.ID); got.Status != "debit_pending" {
		t.Fatalf("release did not admit oldest: %+v", got)
	}
}

func TestPendingTransferTraceLinkHasAFixedWindow(t *testing.T) {
	db := pgtest.NewDatabase(t)
	config := bankConfig(t)
	config.GrafanaURL = "http://grafana.test"
	s, err := transferservice.Open(context.Background(), db, config, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "submit")
	defer span.End()
	submittedAt := time.Now()
	r := httptest.NewRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount":25,"scenario":"bank_b_unavailable"}`)).WithContext(ctx)
	r.AddCookie(&http.Cookie{Name: visitor.CookieName, Value: "visitor"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var submitted transferJSON
	if err := json.Unmarshal(w.Body.Bytes(), &submitted); err != nil || w.Code != http.StatusAccepted {
		t.Fatalf("submit: %d %s, %v", w.Code, w.Body, err)
	}
	page := admissionRequest(s, "visitor", http.MethodGet, "/transfers/"+submitted.ID, "").Body.String()
	match := regexp.MustCompile(`id="trace-link" href="([^"]+)"`).FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("pending page has no trace link:\n%s", page)
	}
	link, err := url.Parse(html.UnescapeString(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	to, err := strconv.ParseInt(link.Query().Get("to"), 10, 64)
	if err != nil || time.UnixMilli(to).Before(submittedAt.Add(config.ResumeWait)) {
		t.Fatalf("pending trace link %s ends at %q, want a fixed time after the scheduled resume", link, link.Query().Get("to"))
	}
}

func TestAdmissionWaitIsTracedOnlyOnceTheAdmissionCommits(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	admissionWaits := func(transferID string) int {
		count := 0
		for _, span := range recorder.Ended() {
			if span.Name() == "admission wait" && slices.Contains(span.Attributes(), attribute.String("saga.transfer_id", transferID)) {
				count++
			}
		}
		return count
	}

	db := pgtest.NewDatabase(t)
	ctx := context.Background()
	s, err := transferservice.Open(ctx, db, bankConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	holder := submitUnavailable(t, s, "holder")
	waiting := submitUnavailable(t, s, "waiting")
	reject := event(t, holder.ID, messaging.DebitRejected{TransferID: holder.ID, Reason: "Insufficient funds", ObservedAt: time.Now()})
	if _, err := db.Exec(ctx, `
		CREATE FUNCTION refuse_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit refused'; END $$;
		CREATE CONSTRAINT TRIGGER refuse_commit AFTER INSERT ON transfer_history DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION refuse_commit();`); err != nil {
		t.Fatalf("install commit trigger: %v", err)
	}
	if err := transferservice.DebitRejected(s, reject); err == nil {
		t.Fatal("DebitRejected with a refused commit succeeded, want an error")
	}
	if got := admissionWaits(waiting.ID); got != 0 {
		t.Fatalf("admission waits after a failed commit = %d, want 0", got)
	}

	if _, err := db.Exec(ctx, `DROP TRIGGER refuse_commit ON transfer_history`); err != nil {
		t.Fatalf("drop commit trigger: %v", err)
	}
	if err := transferservice.DebitRejected(s, reject); err != nil {
		t.Fatal(err)
	}
	if got := admissionTransfer(t, s, "waiting", waiting.ID); got.Status != "debit_pending" {
		t.Fatalf("waiting = %+v, want admitted", got)
	}
	if got := admissionWaits(waiting.ID); got != 1 {
		t.Fatalf("admission waits after the commit = %d, want 1", got)
	}
}
