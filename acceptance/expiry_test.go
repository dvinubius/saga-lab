package acceptance_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/transferservice"
	"github.com/jackc/pgx/v5"
)

func shortExpiry(config *transferservice.Config) {
	config.VisitorExpiry = time.Second
	config.ExpirySweep = 100 * time.Millisecond
}

func TestIdleVisitorExpiresAndStartsOver(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t, shortExpiry)
	earlier := demo.submitTransfer(t, `{"amount": 25}`)
	demo.awaitTransfer(t, earlier.TransferID, "completed")
	id := demo.visitorID(t)

	demo.awaitExpired(t, id)

	demo.assertBalances(t, 100, 0)
	if got := demo.transfers(t); len(got) != 0 {
		t.Fatalf("transfers after expiry = %+v, want none", got)
	}
	if n := count(t, demo.transferService.settings.DatabaseURL, `SELECT count(*) FROM transfer_history WHERE transfer_id = $1`, earlier.TransferID); n != 0 {
		t.Errorf("%d history entries remain for the expired visitor's transfer", n)
	}
}

func TestBusyVisitorsSurviveExpiry(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t, shortExpiry, func(config *transferservice.Config) { config.ResumeWait = time.Minute })
	holderClient := demo.visitor(t)
	holder := holderClient.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	holderClient.awaitCreditConfirmed(t, holder.TransferID)
	waitingClient := demo.visitor(t)
	waiting := waitingClient.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	if waiting.Status != "awaiting_admission" {
		t.Fatalf("waiting = %+v", waiting)
	}
	demo.holdBankADebits(t)
	pending := demo.submitTransfer(t, `{"amount": 25}`)
	idle := demo.visitor(t)

	demo.awaitExpired(t, idle.visitorID(t))

	for _, survivor := range []struct {
		client *visitorClient
		id     string
		status string
	}{
		{demo.visitorClient, pending.TransferID, "debit_pending"},
		{holderClient, holder.TransferID, "credit_pending"},
		{waitingClient, waiting.TransferID, "awaiting_admission"},
	} {
		if got := survivor.client.transfer(t, survivor.id); got.Status != survivor.status {
			t.Errorf("transfer %s after expiry sweep = %+v, want %s", survivor.id, got, survivor.status)
		}
	}
	holderClient.assertBalances(t, 75, 0)
}

func (d *inProcessDemonstration) awaitExpired(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		remaining := count(t, d.transferService.settings.DatabaseURL, `SELECT count(*) FROM visitors WHERE visitor_id = $1`, id) +
			count(t, d.bankA.settings.DatabaseURL, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, id) +
			count(t, d.bankB.settings.DatabaseURL, `SELECT count(*) FROM accounts WHERE visitor_id = $1`, id)
		if remaining == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("visitor %s not expired after the test deadline", id)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestReturningVisitorTopUpSurvivesConcurrentExpiry(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t, func(config *transferservice.Config) {
		config.VisitorExpiry = time.Hour
		config.ExpirySweep = 100 * time.Millisecond
	})
	demo.assertBalances(t, 100, 0)
	db, err := pgx.Connect(context.Background(), demo.transferService.settings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	tx, err := db.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(context.Background(), `SELECT FROM demonstration_slot FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	writer, err := pgx.Connect(context.Background(), demo.transferService.settings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close(context.Background())
	if _, err := writer.Exec(context.Background(), `UPDATE visitors SET last_seen_at = now() - interval '2 hours' WHERE visitor_id = $1`, demo.visitorID(t)); err != nil {
		t.Fatal(err)
	}
	observer, err := pgx.Connect(context.Background(), os.Getenv(pgtest.AdminURLVariable))
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close(context.Background())
	database, _ := url.Parse(demo.transferService.settings.DatabaseURL)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err := observer.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = $1 AND wait_event_type = 'Lock' AND query LIKE '%demonstration_slot FOR UPDATE%')`, strings.TrimPrefix(database.Path, "/")).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expiry did not reach the held demonstration slot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	result := make(chan error, 1)
	go func() {
		r, err := demo.do(http.MethodPost, "/api/top-ups", "", "")
		if err == nil && r.status != http.StatusOK {
			err = fmt.Errorf("top-up status %d, body %q", r.status, r.body)
		}
		result <- err
	}()
	early := false
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if early {
		demo.awaitExpired(t, demo.visitorID(t))
	} else if err := <-result; err != nil {
		t.Fatal(err)
	}
	demo.assertBalances(t, 200, 0)
}

func TestOldCompletedTransfersDisappearWhileAccountsAndPendingTransfersRemain(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)
	old := demo.submitTransfer(t, `{"amount":25}`)
	demo.awaitTransfer(t, old.TransferID, "completed")
	recent := demo.submitTransfer(t, `{"amount":10}`)
	demo.awaitTransfer(t, recent.TransferID, "completed")
	release := demo.holdBankADebits(t)
	pending := demo.submitTransfer(t, `{"amount":5}`)
	db, err := pgx.Connect(context.Background(), demo.transferService.settings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	if _, err := db.Exec(context.Background(), `UPDATE transfers SET requested_at = now() - interval '8 days' WHERE transfer_id = ANY($1)`, []string{old.TransferID, pending.TransferID}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/transfers/", "/transfers/"} {
		if r := demo.request(t, http.MethodGet, path+old.TransferID, "", ""); r.status != http.StatusNotFound {
			t.Errorf("old completed transfer at %s: status %d, want 404", path, r.status)
		}
	}
	if got := demo.transfers(t); len(got) != 2 {
		t.Fatalf("visible transfers = %+v, want recent and pending", got)
	}
	if got := demo.transfer(t, pending.TransferID); got.Status != "debit_pending" {
		t.Fatalf("old pending transfer = %+v", got)
	}
	demo.transferService.stop(t)
	demo.transferService.start(t)
	demo.awaitReady(t, demo.transferService)
	demo.assertBalances(t, 65, 35)
	if got := demo.transfers(t); len(got) != 2 {
		t.Fatalf("visible transfers after retention sweep = %+v", got)
	}
	release()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := demo.request(t, http.MethodGet, "/api/transfers/"+pending.TransferID, "", "")
		if r.status == http.StatusNotFound {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old pending transfer did not finish and disappear")
		}
		time.Sleep(25 * time.Millisecond)
	}
	demo.assertBalances(t, 60, 40)
	if got := demo.transfers(t); len(got) != 1 || got[0].TransferID != recent.TransferID {
		t.Fatalf("transfers after old pending transfer finishes = %+v", got)
	}
}

func TestReturningVisitorWaitsForAccountClosureBeforeReopening(t *testing.T) {
	t.Parallel()
	blocked, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var first atomic.Bool
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	demo := startDemonstration(t, func(config *transferservice.Config) {
		config.VisitorExpiry = time.Hour
		config.ExpirySweep = 100 * time.Millisecond
		a, _ := url.Parse(config.BankAURL)
		b, _ := url.Parse(config.BankBURL)
		bankA := httputil.NewSingleHostReverseProxy(a)
		bankB := httputil.NewSingleHostReverseProxy(b)
		proxyA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && first.CompareAndSwap(false, true) {
				close(blocked)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			bankA.ServeHTTP(w, r)
		}))
		proxyB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bankB.ServeHTTP(w, r)
			if r.Method == http.MethodDelete {
				select {
				case <-release:
					select {
					case finished <- struct{}{}:
					default:
					}
				default:
				}
			}
		}))
		t.Cleanup(proxyA.Close)
		t.Cleanup(proxyB.Close)
		config.BankAURL, config.BankBURL = proxyA.URL, proxyB.URL
	})
	demo.assertBalances(t, 100, 0)
	db, err := pgx.Connect(context.Background(), demo.transferService.settings.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	if _, err := db.Exec(context.Background(), `UPDATE visitors SET last_seen_at = now() - interval '2 hours' WHERE visitor_id = $1`, demo.visitorID(t)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("expiry did not start account closure")
	}
	result := make(chan error, 1)
	go func() {
		r, err := demo.do(http.MethodPost, "/api/top-ups", "", "")
		if err == nil && r.status != http.StatusOK {
			err = fmt.Errorf("top-up status %d, body %q", r.status, r.body)
		}
		result <- err
	}()
	early := false
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
		early = true
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	if early {
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("earlier account closure did not finish")
		}
	} else if err := <-result; err != nil {
		t.Fatal(err)
	}
	demo.assertBalances(t, 200, 0)
}
