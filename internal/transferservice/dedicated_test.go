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

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/transferservice"
)

func TestCreditCommandRoutesToExactlyOneQueue(t *testing.T) {
	for _, scenario := range []string{"bank_b_unavailable", "happy_path", "debit_redelivery", "credit_rejection", "refund_redelivery"} {
		t.Run(scenario, func(t *testing.T) {
			db := pgtest.NewDatabase(t)
			s, err := transferservice.Open(context.Background(), db, bankConfig(t), slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, testRequest(http.MethodPost, "/api/transfers", strings.NewReader(`{"amount":25,"scenario":"`+scenario+`"}`)))
			var submitted struct {
				ID string `json:"transfer_id"`
			}
			if err := json.NewDecoder(response.Body).Decode(&submitted); err != nil || response.Code != http.StatusAccepted {
				t.Fatalf("submit: status %d, body %s, error %v", response.Code, response.Body, err)
			}
			if err := transferservice.FundsDebited(s, event(t, submitted.ID, messaging.FundsDebited{TransferID: submitted.ID, ObservedAt: time.Now()})); err != nil {
				t.Fatal(err)
			}
			topic := "CreditFunds"
			if scenario == "bank_b_unavailable" {
				topic = "CreditFundsDedicated"
			}
			want := state{Status: "credit_pending", Steps: []string{"requested", "debit_committed", "credit_requested"}, Outbox: []string{"DebitFunds", topic}}
			if got := snapshot(t, db, submitted.ID); !reflect.DeepEqual(got, want) {
				t.Fatalf("after debit = %+v, want %+v", got, want)
			}
		})
	}
}
