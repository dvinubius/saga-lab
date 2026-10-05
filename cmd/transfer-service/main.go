package main

import (
	"context"
	"errors"
	"os"
	"time"

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
			wait, err := time.ParseDuration(os.Getenv("BANK_B_RESUME_WAIT"))
			if err != nil || wait <= 0 {
				return errors.New("BANK_B_RESUME_WAIT must be configured as a positive Go duration")
			}
			config.ResumeWait = wait
			return transferservice.Run(ctx, settings, config)
		},
		transferservice.Reset)
}
