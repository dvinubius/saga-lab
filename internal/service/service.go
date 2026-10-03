package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/dvinubius/saga-lab/internal/telemetry"
)

type Settings struct {
	DatabaseURL string
	AMQPURL     string
	Listener    net.Listener
	Logger      *slog.Logger
}

func Main(name string, run, reset func(context.Context, Settings) error) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil)).With("service", name)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	settings := Settings{DatabaseURL: os.Getenv("DATABASE_URL"), AMQPURL: os.Getenv("AMQP_URL"), Logger: logger}

	if len(os.Args) > 1 && os.Args[1] == "reset" {
		if err := reset(ctx, settings); err != nil {
			logger.Error("reset failed", "error", err)
			os.Exit(1)
		}
		logger.Info("reset")
		return
	}

	stopTelemetry, err := telemetry.Start(ctx, name)
	if err == nil {
		settings.Listener, err = net.Listen("tcp", ":8080")
	}
	if err == nil {
		err = errors.Join(run(ctx, settings), stopTelemetry())
	}
	if err != nil {
		logger.Error("stopped", "error", err)
		os.Exit(1)
	}
}
