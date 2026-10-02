package main

import (
	"context"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/service"
)

func main() {
	config := bank.Config{PreparedBalance: 0, Role: bank.Destination}
	service.Main("bank-b",
		func(ctx context.Context) error { return bank.Run(ctx, config) },
		func(ctx context.Context) error { return bank.Reset(ctx, config) })
}
