package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/telemetry"
)

const service = "bank-a"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With("service", service))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stopTelemetry, err := telemetry.Start(ctx, service)
	if err == nil {
		err = errors.Join(bank.Run(ctx, bank.Config{PreparedBalance: 100, Role: bank.Source}), stopTelemetry())
	}
	if err != nil {
		slog.Error("stopped", "error", err)
		os.Exit(1)
	}
}
