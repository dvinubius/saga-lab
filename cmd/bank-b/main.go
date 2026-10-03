package main

import (
	"context"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/service"
)

func main() {
	config := bank.Config{PreparedBalance: 0, Role: bank.Destination}
	service.Main("bank-b",
		func(ctx context.Context, settings service.Settings) error { return bank.Run(ctx, settings, config) },
		func(ctx context.Context, settings service.Settings) error { return bank.Reset(ctx, settings, config) })
}
