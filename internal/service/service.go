package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dvinubius/saga-lab/internal/telemetry"
)

func Main(name string, run, reset func(context.Context) error) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With("service", name))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) > 1 && os.Args[1] == "reset" {
		if err := reset(ctx); err != nil {
			slog.Error("reset failed", "error", err)
			os.Exit(1)
		}
		slog.Info("reset")
		return
	}

	stopTelemetry, err := telemetry.Start(ctx, name)
	if err == nil {
		err = errors.Join(run(ctx), stopTelemetry())
	}
	if err != nil {
		slog.Error("stopped", "error", err)
		os.Exit(1)
	}
}
