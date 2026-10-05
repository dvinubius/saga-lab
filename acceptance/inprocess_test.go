package acceptance_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/dvinubius/saga-lab/internal/service"
	"github.com/dvinubius/saga-lab/internal/transferservice"
)

const (
	amqpURLVariable       = "SAGA_LAB_AMQP_URL"
	managementURLVariable = "SAGA_LAB_RABBITMQ_MANAGEMENT_URL"
	readinessDeadline     = 30 * time.Second
	stopDeadline          = 10 * time.Second
)

var (
	bankAConfig = bank.Config{OpeningBalance: 100, Role: bank.Source}
	bankBConfig = bank.Config{OpeningBalance: 0, Role: bank.Destination}
)

type inProcessDemonstration struct {
	demonstration
	bankA, bankB, transferService *inProcessService
}

type inProcessService struct {
	name     string
	address  string
	run      func(context.Context, service.Settings) error
	settings service.Settings
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

func startDemonstration(t *testing.T) *inProcessDemonstration {
	t.Helper()
	for _, variable := range []string{pgtest.AdminURLVariable, amqpURLVariable, managementURLVariable} {
		if os.Getenv(variable) == "" {
			t.Skipf("%s is not set; run scripts/test.sh", variable)
		}
	}
	id := randomHex(t)
	t.Logf("demonstration %s", id)
	amqpURL := newVirtualHost(t, "demonstration-"+id)
	logs := &logBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	newService := func(name, database string, run func(context.Context, service.Settings) error) *inProcessService {
		t.Helper()
		databaseURL := pgtest.NewServiceDatabase(t, database+"_"+id)
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen for %s: %v", name, err)
		}
		s := &inProcessService{name: name, address: listener.Addr().String(), run: run, settings: service.Settings{
			DatabaseURL: databaseURL,
			AMQPURL:     amqpURL,
			Listener:    listener,
			Logger:      logger.With("service", name),
		}}
		t.Cleanup(func() {
			if s.settings.Listener != nil {
				s.settings.Listener.Close()
			}
		})
		return s
	}

	bankA := newService("bank-a", "bank_a", func(ctx context.Context, settings service.Settings) error {
		return bank.Run(ctx, settings, bankAConfig)
	})
	bankB := newService("bank-b", "bank_b", func(ctx context.Context, settings service.Settings) error {
		return bank.Run(ctx, settings, bankBConfig)
	})
	config := transferservice.Config{ResumeWait: 2500 * time.Millisecond, BankAURL: bankA.url(), BankBURL: bankB.url(), GrafanaURL: "http://localhost:3000"}
	transferService := newService("transfer-service", "transfer_service", func(ctx context.Context, settings service.Settings) error {
		return transferservice.Run(ctx, settings, config)
	})
	d := &inProcessDemonstration{
		demonstration:   newDemonstration(transferService.url()),
		bankA:           bankA,
		bankB:           bankB,
		transferService: transferService,
	}

	t.Cleanup(func() {
		d.stop(t)
		if t.Failed() {
			t.Logf("demonstration %s logs:\n%s", id, logs)
		}
	})
	d.start(t)
	return d
}

func (d *inProcessDemonstration) start(t *testing.T) {
	t.Helper()
	d.bankA.start(t)
	d.bankB.start(t)
	d.awaitReady(t, d.bankA)
	d.awaitReady(t, d.bankB)
	d.transferService.start(t)
	d.awaitReady(t, d.transferService)
}

func (d *inProcessDemonstration) stop(t *testing.T) bool {
	t.Helper()
	var stopping sync.WaitGroup
	var failed atomic.Bool
	for _, s := range []*inProcessService{d.transferService, d.bankB, d.bankA} {
		stopping.Go(func() {
			if !s.stop(t) {
				failed.Store(true)
			}
		})
	}
	stopping.Wait()
	return !failed.Load()
}

func (d *inProcessDemonstration) restart(t *testing.T) {
	t.Helper()
	if !d.stop(t) {
		t.FailNow()
	}
	d.start(t)
}

func (d *inProcessDemonstration) reset(t *testing.T) {
	t.Helper()
	if !d.stop(t) {
		t.FailNow()
	}
	ctx := context.Background()
	if err := transferservice.Reset(ctx, d.transferService.settings); err != nil {
		t.Fatalf("reset transfer-service: %v", err)
	}
	if err := bank.Reset(ctx, d.bankA.settings, bankAConfig); err != nil {
		t.Fatalf("reset bank-a: %v", err)
	}
	if err := bank.Reset(ctx, d.bankB.settings, bankBConfig); err != nil {
		t.Fatalf("reset bank-b: %v", err)
	}
	d.start(t)
}

func (s *inProcessService) url() string {
	return "http://" + s.address
}

func (s *inProcessService) start(t *testing.T) {
	t.Helper()
	if s.settings.Listener == nil {
		listener, err := net.Listen("tcp", s.address)
		if err != nil {
			t.Fatalf("listen for %s: %v", s.name, err)
		}
		s.settings.Listener = listener
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	done := make(chan struct{})
	s.done = done
	go func() {
		s.err = s.run(ctx, s.settings)
		close(done)
	}()
}

func (s *inProcessService) stop(t *testing.T) bool {
	t.Helper()
	if s.done == nil {
		return true
	}
	done := s.done
	s.done = nil
	s.settings.Listener = nil
	select {
	case <-done:
		t.Errorf("%s stopped before the test ended: %v", s.name, s.err)
		return false
	default:
	}
	s.cancel()
	select {
	case <-done:
	case <-time.After(stopDeadline):
		t.Errorf("%s did not stop within %v", s.name, stopDeadline)
		return false
	}
	if s.err != nil {
		t.Errorf("%s stopped with an error: %v", s.name, s.err)
		return false
	}
	return true
}

func (d *inProcessDemonstration) awaitReady(t *testing.T, s *inProcessService) {
	t.Helper()
	deadline := time.Now().Add(readinessDeadline)
	for {
		if r, err := d.client.Get(s.url() + "/readyz"); err == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s not ready after %v", s.name, readinessDeadline)
		}
		select {
		case <-s.done:
			t.Fatalf("%s stopped before it was ready: %v", s.name, s.err)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func newVirtualHost(t *testing.T, name string) string {
	t.Helper()
	amqpURL, err := url.Parse(os.Getenv(amqpURLVariable))
	if err != nil {
		t.Fatalf("parse %s: %v", amqpURLVariable, err)
	}
	vhost := "/api/vhosts/" + url.PathEscape(name)
	if err := manageRabbitMQ(http.MethodPut, vhost, "{}"); err != nil {
		t.Fatalf("create virtual host %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := manageRabbitMQ(http.MethodDelete, vhost, ""); err != nil {
			t.Errorf("delete virtual host %s: %v", name, err)
		}
	})
	permissions := "/api/permissions/" + url.PathEscape(name) + "/" + url.PathEscape(amqpURL.User.Username())
	if err := manageRabbitMQ(http.MethodPut, permissions, `{"configure": ".*", "write": ".*", "read": ".*"}`); err != nil {
		t.Fatalf("grant access to virtual host %s: %v", name, err)
	}
	amqpURL.Path = "/" + name
	return amqpURL.String()
}

func manageRabbitMQ(method, path, body string) error {
	request, err := http.NewRequest(method, os.Getenv(managementURLVariable)+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	r, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode/100 != 2 {
		message, _ := io.ReadAll(r.Body)
		return fmt.Errorf("%s %s: status %d, body %q", method, path, r.StatusCode, message)
	}
	return nil
}

type logBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
