package main

import (
	"context"
	"errors"
	"os"

	"github.com/dvinubius/saga-lab/internal/service"
	"github.com/dvinubius/saga-lab/internal/transferservice"
)

func main() {
	config := transferservice.Config{BankAURL: os.Getenv("BANK_A_URL"), BankBURL: os.Getenv("BANK_B_URL"), GrafanaURL: os.Getenv("GRAFANA_URL")}
	service.Main("transfer-service",
		func(ctx context.Context, settings service.Settings) error {
			if config.BankAURL == "" || config.BankBURL == "" || config.GrafanaURL == "" {
				return errors.New("BANK_A_URL, BANK_B_URL and GRAFANA_URL must be configured")
			}
			return transferservice.Run(ctx, settings, config)
		},
		transferservice.Reset)
}
