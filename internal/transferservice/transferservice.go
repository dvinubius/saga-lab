package transferservice

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

//go:embed schema.sql
var schema string

//go:embed pages.html
var pagesTemplate string

var pages = template.Must(template.New("pages").Parse(pagesTemplate))

type Config struct {
	BankAURL string
	BankBURL string
}

func Run(ctx context.Context) error {
	config := Config{BankAURL: os.Getenv("BANK_A_URL"), BankBURL: os.Getenv("BANK_B_URL")}
	if config.BankAURL == "" || config.BankBURL == "" {
		return errors.New("BANK_A_URL and BANK_B_URL must be configured")
	}
	db, err := postgres.Connect(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	broker, err := messaging.Connect(os.Getenv("AMQP_URL"), messaging.DebitFundsTopic, messaging.CreditFundsTopic)
	if err != nil {
		return err
	}
	defer broker.Close()
	s, err := Open(ctx, db, broker, config)
	if err != nil {
		return err
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return broker.Run(ctx) })
	g.Go(func() error { return web.Serve(ctx, ":8080", s.Handler()) })
	return g.Wait()
}

type Service struct {
	db     *pgxpool.Pool
	broker *messaging.Broker
	bankA  bankClient
	bankB  bankClient
}

func Open(ctx context.Context, db *pgxpool.Pool, broker *messaging.Broker, config Config) (*Service, error) {
	if _, err := db.Exec(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	s := &Service{
		db:     db,
		broker: broker,
		bankA:  newBankClient("Bank A", config.BankAURL),
		bankB:  newBankClient("Bank B", config.BankBURL),
	}
	broker.Router.AddHandler("funds-debited",
		messaging.FundsDebitedTopic, broker.Subscriber,
		messaging.CreditFundsTopic, broker.Publisher,
		s.fundsDebited)
	broker.Router.AddConsumerHandler("funds-credited",
		messaging.FundsCreditedTopic, broker.Subscriber,
		s.fundsCredited)
	return s, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.getHome)
	mux.HandleFunc("POST /transfers", s.postTransferForm)
	mux.HandleFunc("GET /transfers/{transferID}", s.getTransferPage)
	mux.HandleFunc("GET /api/balances", s.getBalances)
	mux.HandleFunc("GET /api/transfers", s.getTransfers)
	mux.HandleFunc("POST /api/transfers", s.postTransfer)
	mux.HandleFunc("GET /api/transfers/{transferID}", s.getTransfer)
	mux.Handle("GET /readyz", web.Readiness(s.ready))
	return mux
}

func (s *Service) ready(ctx context.Context) error {
	if err := s.broker.Ready(); err != nil {
		return err
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
	bankA, err := s.bankA.balance(ctx, visitor.PreparedID)
	if err != nil {
		return balances{}, err
	}
	bankB, err := s.bankB.balance(ctx, visitor.PreparedID)
	if err != nil {
		return balances{}, err
	}
	return balances{BankA: accountBalance{bankA}, BankB: accountBalance{bankB}}, nil
}

func (s *Service) getBalances(w http.ResponseWriter, r *http.Request) {
	b, err := s.balances(r.Context())
	if err != nil {
		slog.Error("read balances", "error", err)
		web.WriteError(w, http.StatusBadGateway, "balances unavailable")
		return
	}
	web.WriteJSON(w, http.StatusOK, b)
}

func (s *Service) getTransfers(w http.ResponseWriter, r *http.Request) {
	transfers, err := s.list(r.Context())
	if err != nil {
		slog.Error("list transfers", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "transfers unavailable")
		return
	}
	web.WriteJSON(w, http.StatusOK, map[string][]transfer{"transfers": transfers})
}

func (s *Service) postTransfer(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Amount json.RawMessage `json:"amount"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	if err := decoder.Decode(&request); err != nil || !errors.Is(decoder.Decode(new(json.RawMessage)), io.EOF) {
		web.WriteError(w, http.StatusBadRequest, "request body must be a JSON object with an amount")
		return
	}
	amount, err := parseAmount(string(request.Amount))
	if err != nil {
		web.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	t, err := s.submit(r.Context(), amount)
	if err != nil {
		slog.Error("submit transfer", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "transfer could not be started")
		return
	}
	w.Header().Set("Location", "/api/transfers/"+t.ID)
	web.WriteJSON(w, http.StatusAccepted, t)
}

func (s *Service) getTransfer(w http.ResponseWriter, r *http.Request) {
	t, err := s.find(r.Context(), r.PathValue("transferID"))
	if errors.Is(err, errTransferNotFound) {
		web.WriteError(w, http.StatusNotFound, "transfer not found")
		return
	}
	if err != nil {
		slog.Error("read transfer", "error", err)
		web.WriteError(w, http.StatusInternalServerError, "transfer unavailable")
		return
	}
	web.WriteJSON(w, http.StatusOK, t)
}

type homePage struct {
	Balances  balances
	Transfers []transfer
	Amount    string
	Error     string
}

type transferPage struct {
	Balances balances
	Transfer transfer
}

func (s *Service) getHome(w http.ResponseWriter, r *http.Request) {
	s.renderHome(w, r, http.StatusOK, homePage{})
}

func (s *Service) renderHome(w http.ResponseWriter, r *http.Request, status int, page homePage) {
	var err error
	if page.Balances, err = s.balances(r.Context()); err != nil {
		slog.Error("read balances", "error", err)
		http.Error(w, "Balances are temporarily unavailable.", http.StatusBadGateway)
		return
	}
	if page.Transfers, err = s.list(r.Context()); err != nil {
		slog.Error("list transfers", "error", err)
		http.Error(w, "Transfers are temporarily unavailable.", http.StatusInternalServerError)
		return
	}
	render(w, status, "home", page)
}

func (s *Service) postTransferForm(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.PostFormValue("amount"))
	amount, err := parseAmount(text)
	if err != nil {
		s.renderHome(w, r, http.StatusBadRequest, homePage{Amount: text, Error: err.Error()})
		return
	}
	t, err := s.submit(r.Context(), amount)
	if err != nil {
		slog.Error("submit transfer", "error", err)
		http.Error(w, "The transfer could not be started.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/transfers/"+t.ID, http.StatusSeeOther)
}

func (s *Service) getTransferPage(w http.ResponseWriter, r *http.Request) {
	t, err := s.find(r.Context(), r.PathValue("transferID"))
	if errors.Is(err, errTransferNotFound) {
		http.Error(w, "Transfer not found.", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("read transfer", "error", err)
		http.Error(w, "The transfer is temporarily unavailable.", http.StatusInternalServerError)
		return
	}
	b, err := s.balances(r.Context())
	if err != nil {
		slog.Error("read balances", "error", err)
		http.Error(w, "Balances are temporarily unavailable.", http.StatusBadGateway)
		return
	}
	render(w, http.StatusOK, "transfer", transferPage{Balances: b, Transfer: t})
}

func render(w http.ResponseWriter, status int, name string, data any) {
	var body bytes.Buffer
	if err := pages.ExecuteTemplate(&body, name, data); err != nil {
		slog.Error("render page", "page", name, "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	body.WriteTo(w)
}
