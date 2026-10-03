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

type inProcessDemonstration struct {
	demonstration
	bankA, bankB, transferService *inProcessService
}

type inProcessService struct {
	name     string
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
		return &inProcessService{name: name, run: run, settings: service.Settings{
			DatabaseURL: databaseURL,
			AMQPURL:     amqpURL,
			Listener:    listener,
			Logger:      logger.With("service", name),
		}}
	}

	bankA := newService("bank-a", "bank_a", func(ctx context.Context, settings service.Settings) error {
		return bank.Run(ctx, settings, bank.Config{PreparedBalance: 100, Role: bank.Source})
	})
	bankB := newService("bank-b", "bank_b", func(ctx context.Context, settings service.Settings) error {
		return bank.Run(ctx, settings, bank.Config{PreparedBalance: 0, Role: bank.Destination})
	})
	config := transferservice.Config{BankAURL: bankA.url(), BankBURL: bankB.url(), GrafanaURL: "http://localhost:3000"}
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
		var stopping sync.WaitGroup
		for _, s := range []*inProcessService{transferService, bankB, bankA} {
			stopping.Go(func() { s.stop(t) })
		}
		stopping.Wait()
		if t.Failed() {
			t.Logf("demonstration %s logs:\n%s", id, logs)
		}
	})
	bankA.start()
	bankB.start()
	d.awaitReady(t, bankA)
	d.awaitReady(t, bankB)
	transferService.start()
	d.awaitReady(t, transferService)
	return d
}

func (s *inProcessService) url() string {
	return "http://" + s.settings.Listener.Addr().String()
}

func (s *inProcessService) start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		s.err = s.run(ctx, s.settings)
		close(s.done)
	}()
}

func (s *inProcessService) stop(t *testing.T) {
	t.Helper()
	if s.done == nil {
		return
	}
	select {
	case <-s.done:
		t.Errorf("%s stopped before the test ended: %v", s.name, s.err)
		return
	default:
	}
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(stopDeadline):
		t.Errorf("%s did not stop within %v", s.name, stopDeadline)
		return
	}
	if s.err != nil {
		t.Errorf("%s stopped with an error: %v", s.name, s.err)
	}
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
			t.Fatalf("%s stopped before it was ready", s.name)
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
