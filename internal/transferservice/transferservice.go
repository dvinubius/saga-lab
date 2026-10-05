package transferservice

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/service"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

//go:embed schema.sql
var schema string

//go:embed pages.html
var pagesTemplate string

//go:embed static
var static embed.FS

var pages = template.Must(template.New("pages").Parse(pagesTemplate))

type Config struct {
	BankAURL   string
	BankBURL   string
	GrafanaURL string
	ResumeWait time.Duration
}

func Run(ctx context.Context, settings service.Settings, config Config) error {
	defer settings.Listener.Close()
	db, err := postgres.Connect(ctx, settings.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	s, err := Open(ctx, db, config, settings.Logger)
	if err != nil {
		return err
	}
	broker, err := messaging.Connect(settings.AMQPURL, settings.Logger, messaging.DebitFundsTopic, messaging.CreditFundsTopic, messaging.CreditFundsDedicatedTopic, messaging.RefundFundsTopic, messaging.ResumeDeliveryTopic)
	if err != nil {
		return err
	}
	defer broker.Close()
	s.attachBroker(broker)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return broker.Run(ctx) })
	g.Go(func() error { return broker.RunRelay(ctx, db, settings.Logger) })
	g.Go(func() error { return s.runResumeSchedule(ctx) })
	g.Go(func() error { return web.Serve(ctx, settings.Listener, s.Handler(), web.Public, settings.Logger) })
	return g.Wait()
}

func Reset(ctx context.Context, settings service.Settings) error {
	if err := messaging.Purge(settings.AMQPURL, messaging.FundsDebitedTopic, messaging.DebitRejectedTopic, messaging.FundsCreditedTopic, messaging.CreditRejectedTopic, messaging.FundsRefundedTopic, messaging.ProcessingObservedTopic); err != nil {
		return err
	}
	db, err := postgres.Connect(ctx, settings.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DROP TABLE IF EXISTS demonstration_slot, transfer_history, transfers, visitors`); err != nil {
			return fmt.Errorf("drop transfers: %w", err)
		}
		if _, err := tx.Exec(ctx, schema); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		return messaging.Outbox.Recreate(ctx, tx)
	})
}

type Service struct {
	db         *pgxpool.Pool
	broker     *messaging.Broker
	bankA      bankClient
	bankB      bankClient
	grafanaURL string
	logger     *slog.Logger
	resumeWait time.Duration
}

func Open(ctx context.Context, db *pgxpool.Pool, config Config, logger *slog.Logger) (*Service, error) {
	if config.ResumeWait <= 0 {
		return nil, errors.New("Bank B resume wait must be positive")
	}
	if _, err := db.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := messaging.Outbox.Create(ctx, db); err != nil {
		return nil, err
	}
	return &Service{
		db:         db,
		bankA:      newBankClient(messaging.BankA, config.BankAURL),
		bankB:      newBankClient(messaging.BankB, config.BankBURL),
		grafanaURL: strings.TrimSuffix(config.GrafanaURL, "/"),
		logger:     logger,
		resumeWait: config.ResumeWait,
	}, nil
}

func (s *Service) attachBroker(broker *messaging.Broker) {
	s.broker = broker
	broker.OnConfirmed(messaging.CreditFundsDedicatedTopic, s.creditConfirmed)
	broker.Router.AddConsumerHandler("funds-debited",
		messaging.FundsDebitedTopic, broker.Subscriber,
		s.fundsDebited)
	broker.Router.AddConsumerHandler("debit-rejected",
		messaging.DebitRejectedTopic, broker.Subscriber,
		s.debitRejected)
	broker.Router.AddConsumerHandler("funds-credited",
		messaging.FundsCreditedTopic, broker.Subscriber,
		s.fundsCredited)
	broker.Router.AddConsumerHandler("credit-rejected",
		messaging.CreditRejectedTopic, broker.Subscriber,
		s.creditRejected)
	broker.Router.AddConsumerHandler("funds-refunded",
		messaging.FundsRefundedTopic, broker.Subscriber,
		s.fundsRefunded)
	broker.Router.AddConsumerHandler("processing-observed",
		messaging.ProcessingObservedTopic, broker.Subscriber,
		s.processingObserved)
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.getHome)
	mux.HandleFunc("POST /transfers", s.postTransferForm)
	mux.HandleFunc("POST /top-ups", s.postTopUpForm)
	mux.HandleFunc("GET /transfers/{transferID}", s.getTransferPage)
	mux.HandleFunc("GET /api/balances", s.getBalances)
	mux.HandleFunc("POST /api/top-ups", s.postTopUp)
	mux.HandleFunc("GET /api/transfers", s.getTransfers)
	mux.HandleFunc("POST /api/transfers", s.postTransfer)
	mux.HandleFunc("GET /api/transfers/{transferID}", s.getTransfer)
	root := http.NewServeMux()
	root.Handle("GET /readyz", web.Readiness(s.ready, s.logger))
	root.Handle("GET /static/", staticFiles())
	root.Handle("/", s.visitors(mux))
	return root
}

func staticFiles() http.Handler {
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/fonts/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Service) ready(ctx context.Context) error {
	if s.broker != nil {
		if err := s.broker.Ready(); err != nil {
			return err
		}
	}
	return s.db.Ping(ctx)
}

type accountBalance struct {
	Balance int64 `json:"balance"`
}

type balances struct {
	BankA accountBalance `json:"bank_a"`
	BankB accountBalance `json:"bank_b"`
}

func (s *Service) balances(ctx context.Context) (balances, error) {
	bankA, err := s.bankA.balance(ctx, visitor.ID(ctx))
	if err != nil {
		return balances{}, err
	}
	bankB, err := s.bankB.balance(ctx, visitor.ID(ctx))
	if err != nil {
		return balances{}, err
	}
	return balances{BankA: accountBalance{bankA}, BankB: accountBalance{bankB}}, nil
}

func (s *Service) getBalances(w http.ResponseWriter, r *http.Request) {
	b, err := s.balances(r.Context())
	if err != nil {
		s.logger.Error("read balances", "error", err)
		web.WriteError(w, http.StatusBadGateway, "balances unavailable", s.logger)
		return
	}
	web.WriteJSON(w, http.StatusOK, b, s.logger)
}

func (s *Service) topUp(ctx context.Context) (balances, error) {
	pending, err := s.pendingTransferID(ctx)
	if err != nil {
		return balances{}, fmt.Errorf("find pending transfer: %w", err)
	}
	if pending != "" {
		return balances{}, pendingTransferError{PendingID: pending}
	}
	bankA, err := s.bankA.topUp(ctx, visitor.ID(ctx))
	if err != nil {
		return balances{}, err
	}
	bankB, err := s.bankB.balance(ctx, visitor.ID(ctx))
	if err != nil {
		return balances{}, err
	}
	return balances{BankA: accountBalance{bankA}, BankB: accountBalance{bankB}}, nil
}

func (s *Service) postTopUp(w http.ResponseWriter, r *http.Request) {
	b, err := s.topUp(r.Context())
	if pending, ok := errors.AsType[pendingTransferError](err); ok {
		web.WriteJSON(w, http.StatusConflict, map[string]string{
			"error":               "a transfer is still pending; top up once it has finished",
			"pending_transfer_id": pending.PendingID,
		}, s.logger)
		return
	}
	if err != nil {
		s.logger.Error("top up", "error", err)
		web.WriteError(w, http.StatusBadGateway, "top-up unavailable", s.logger)
		return
	}
	web.WriteJSON(w, http.StatusOK, b, s.logger)
}

func (s *Service) getTransfers(w http.ResponseWriter, r *http.Request) {
	transfers, err := s.list(r.Context())
	if err != nil {
		s.logger.Error("list transfers", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "transfers unavailable", s.logger)
		return
	}
	web.WriteJSON(w, http.StatusOK, map[string][]transferSummary{"transfers": transfers}, s.logger)
}

func (s *Service) postTransfer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Amount   json.RawMessage `json:"amount"`
		Scenario *string         `json:"scenario"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(new(json.RawMessage)), io.EOF) {
		web.WriteError(w, http.StatusBadRequest, "request body must be a JSON object with an amount", s.logger)
		return
	}
	amount, err := parseAmount(string(request.Amount))
	if err != nil {
		web.WriteError(w, http.StatusBadRequest, err.Error(), s.logger)
		return
	}
	chosen := happyPath
	if request.Scenario != nil {
		if chosen, err = parseScenario(*request.Scenario); err != nil {
			web.WriteError(w, http.StatusBadRequest, err.Error(), s.logger)
			return
		}
	}
	t, err := s.submit(r.Context(), amount, chosen)
	if pending, ok := errors.AsType[pendingTransferError](err); ok {
		web.WriteJSON(w, http.StatusConflict, map[string]string{
			"error":               "another transfer is still pending; submit again once it has finished",
			"pending_transfer_id": pending.PendingID,
		}, s.logger)
		return
	}
	if err != nil {
		s.logger.Error("submit transfer", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "transfer could not be started", s.logger)
		return
	}
	w.Header().Set("Location", "/api/transfers/"+t.ID)
	web.WriteJSON(w, http.StatusAccepted, t, s.logger)
}

func (s *Service) getTransfer(w http.ResponseWriter, r *http.Request) {
	t, err := s.find(r.Context(), r.PathValue("transferID"))
	if errors.Is(err, errTransferNotFound) {
		web.WriteError(w, http.StatusNotFound, "transfer not found", s.logger)
		return
	}
	if err != nil {
		s.logger.Error("read transfer", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "transfer unavailable", s.logger)
		return
	}
	web.WriteJSON(w, http.StatusOK, t, s.logger)
}

type homePage struct {
	Balances      balances
	Transfers     []transferSummary
	PendingID     string
	Amount        string
	Scenario      scenario
	Scenarios     []scenario
	Error         string
	ScenarioError string
	Overlap       bool
	TopUpRefused  bool
}

type transferPage struct {
	Balances balances
	Transfer transfer
	Lanes    []string
	History  []historyRow
	TraceURL string
}

func (s *Service) getHome(w http.ResponseWriter, r *http.Request) {
	s.renderHome(w, r, http.StatusOK, homePage{})
}

func (s *Service) renderHome(w http.ResponseWriter, r *http.Request, status int, page homePage) {
	var err error
	if page.Balances, err = s.balances(r.Context()); err != nil {
		s.logger.Error("read balances", "error", err)
		http.Error(w, "Balances are temporarily unavailable.", http.StatusBadGateway)
		return
	}
	if page.Transfers, err = s.list(r.Context()); err != nil {
		s.logger.Error("list transfers", "error", err)
		http.Error(w, "Transfers are temporarily unavailable.", http.StatusInternalServerError)
		return
	}
	if i := slices.IndexFunc(page.Transfers, func(t transferSummary) bool { return t.Status.Pending() }); i >= 0 && page.PendingID == "" {
		page.PendingID = page.Transfers[i].ID
	}
	page.Scenarios = scenarios
	if page.Scenario == "" {
		page.Scenario = happyPath
	}
	s.render(w, status, "home", page)
}

func (s *Service) postTransferForm(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.PostFormValue("amount"))
	slug := string(happyPath)
	if r.PostForm.Has("scenario") {
		slug = r.PostForm.Get("scenario")
	}
	chosen, err := parseScenario(slug)
	if err != nil {
		s.renderHome(w, r, http.StatusBadRequest, homePage{Amount: text, ScenarioError: err.Error()})
		return
	}
	amount, err := parseAmount(text)
	if err != nil {
		s.renderHome(w, r, http.StatusBadRequest, homePage{Amount: text, Scenario: chosen, Error: err.Error()})
		return
	}
	t, err := s.submit(r.Context(), amount, chosen)
	if pending, ok := errors.AsType[pendingTransferError](err); ok {
		s.renderHome(w, r, http.StatusConflict, homePage{PendingID: pending.PendingID, Amount: text, Scenario: chosen, Overlap: true})
		return
	}
	if err != nil {
		s.logger.Error("submit transfer", "error", err)
		http.Error(w, "The transfer could not be started.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/transfers/"+t.ID, http.StatusSeeOther)
}

func (s *Service) postTopUpForm(w http.ResponseWriter, r *http.Request) {
	_, err := s.topUp(r.Context())
	if pending, ok := errors.AsType[pendingTransferError](err); ok {
		s.renderHome(w, r, http.StatusConflict, homePage{PendingID: pending.PendingID, TopUpRefused: true})
		return
	}
	if err != nil {
		s.logger.Error("top up", "error", err)
		http.Error(w, "The top-up could not be applied.", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Service) getTransferPage(w http.ResponseWriter, r *http.Request) {
	t, err := s.find(r.Context(), r.PathValue("transferID"))
	if errors.Is(err, errTransferNotFound) {
		http.Error(w, "Transfer not found.", http.StatusNotFound)
		return
	}
	if err != nil {
		s.logger.Error("read transfer", "error", err)
		http.Error(w, "The transfer is temporarily unavailable.", http.StatusInternalServerError)
		return
	}
	b, err := s.balances(r.Context())
	if err != nil {
		s.logger.Error("read balances", "error", err)
		http.Error(w, "Balances are temporarily unavailable.", http.StatusBadGateway)
		return
	}
	page := transferPage{Balances: b, Transfer: t, Lanes: lanes, History: playback(historyRows(t.History))}
	if t.TraceID != "" {
		page.TraceURL = s.traceURL(t.TraceID)
	}
	s.render(w, http.StatusOK, "transfer", page)
}

func (s *Service) traceURL(traceID string) string {
	panes, _ := json.Marshal(map[string]any{
		"trace": map[string]any{
			"datasource": "tempo",
			"queries": []map[string]any{{
				"refId":      "A",
				"datasource": map[string]string{"type": "tempo", "uid": "tempo"},
				"queryType":  "traceql",
				"query":      traceID,
			}},
			"range": map[string]string{"from": "now-1h", "to": "now"},
		},
	})
	query := url.Values{"schemaVersion": {"1"}, "orgId": {"1"}, "panes": {string(panes)}}
	return s.grafanaURL + "/explore?" + query.Encode()
}

func (s *Service) render(w http.ResponseWriter, status int, name string, data any) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, name, data); err != nil {
		s.logger.Error("render page", "page", name, "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	body.WriteTo(w)
}
