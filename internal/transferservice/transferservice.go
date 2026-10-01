package transferservice

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"os"

	"github.com/dvinubius/saga-lab/internal/postgres"
	"github.com/dvinubius/saga-lab/internal/visitor"
	"github.com/dvinubius/saga-lab/internal/web"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed page.html
var pageTemplate string

var page = template.Must(template.New("page").Parse(pageTemplate))

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
	return web.Serve(ctx, ":8080", New(db, config).Handler())
}

type Service struct {
	db    *pgxpool.Pool
	bankA bankClient
	bankB bankClient
}

func New(db *pgxpool.Pool, config Config) *Service {
	return &Service{
		db:    db,
		bankA: newBankClient("Bank A", config.BankAURL),
		bankB: newBankClient("Bank B", config.BankBURL),
	}
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.getPage)
	mux.HandleFunc("GET /api/balances", s.getBalances)
	mux.Handle("GET /readyz", web.Readiness(s.db.Ping))
	return mux
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

func (s *Service) getPage(w http.ResponseWriter, r *http.Request) {
	b, err := s.balances(r.Context())
	if err != nil {
		slog.Error("read balances", "error", err)
		http.Error(w, "Balances are temporarily unavailable.", http.StatusBadGateway)
		return
	}
	var body bytes.Buffer
	if err := page.Execute(&body, b); err != nil {
		slog.Error("render page", "error", err)
		http.Error(w, "The page could not be rendered.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	body.WriteTo(w)
}
