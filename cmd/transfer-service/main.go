package main

import (
	"github.com/dvinubius/saga-lab/internal/service"
	"github.com/dvinubius/saga-lab/internal/transferservice"
)

func main() {
	service.Main("transfer-service", transferservice.Run, transferservice.Reset)
}
