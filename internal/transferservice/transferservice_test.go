package transferservice_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/transferservice"
)

func TestEventsForUnknownTransferAreIgnoredWithoutBroker(t *testing.T) {
	db := pgtest.NewDatabase(t)
	s, err := transferservice.Open(context.Background(), db, transferservice.Config{}, slog.New(slog.NewTextHandler(t.Output(), nil)))
	if err != nil {
		t.Fatalf("open transfer service: %v", err)
	}

	commands, err := transferservice.FundsDebited(s, event(t, messaging.FundsDebited{TransferID: "unknown", ObservedAt: time.Now()}))
	if err != nil || len(commands) != 0 {
		t.Fatalf("FundsDebited = %v, %v; want no commands, no error", commands, err)
	}
	if err := transferservice.DebitRejected(s, event(t, messaging.DebitRejected{TransferID: "unknown", Reason: "Insufficient funds", ObservedAt: time.Now()})); err != nil {
		t.Fatalf("DebitRejected: %v", err)
	}
	if err := transferservice.FundsCredited(s, event(t, messaging.FundsCredited{TransferID: "unknown", ObservedAt: time.Now()})); err != nil {
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

func event(t *testing.T, payload any) *message.Message {
	t.Helper()
	msg, err := messaging.New(context.Background(), "unknown", payload, "")
	if err != nil {
		t.Fatalf("new event: %v", err)
	}
	return msg
}
