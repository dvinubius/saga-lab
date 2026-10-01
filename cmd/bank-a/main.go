package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dvinubius/saga-lab/internal/bank"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With("service", "bank-a"))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := bank.Run(ctx, bank.Config{PreparedBalance: 100, Role: bank.Source}); err != nil {
		slog.Error("stopped", "error", err)
		os.Exit(1)
	}
}
