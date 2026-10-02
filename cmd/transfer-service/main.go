package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dvinubius/saga-lab/internal/telemetry"
	"github.com/dvinubius/saga-lab/internal/transferservice"
)

const service = "transfer-service"

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With("service", service))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && os.Args[1] == "reset" {
		if err := transferservice.Reset(ctx); err != nil {
			slog.Error("reset failed", "error", err)
			os.Exit(1)
		}
		slog.Info("reset")
		return
	}

	stopTelemetry, err := telemetry.Start(ctx, service)
	if err == nil {
		err = errors.Join(transferservice.Run(ctx), stopTelemetry())
	}
	if err != nil {
		slog.Error("stopped", "error", err)
		os.Exit(1)
	}
}
