package main

import (
	"context"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/service"
)

func main() {
	config := bank.Config{PreparedBalance: 100, Role: bank.Source}
	service.Main("bank-a",
		func(ctx context.Context) error { return bank.Run(ctx, config) },
		func(ctx context.Context) error { return bank.Reset(ctx, config) })
}
