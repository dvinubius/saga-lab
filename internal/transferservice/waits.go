package transferservice

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	admissionWaitSpan = "admission wait"
	deliveryWaitSpan  = "delivery wait (scheduled)"
)

var tracer = otel.Tracer("github.com/dvinubius/saga-lab/internal/transferservice")

type wait struct {
	ctx        context.Context
	name       string
	transferID string
	start, end time.Time
}

func (w wait) record() {
	if w.ctx == nil {
		return
	}
	_, span := tracer.Start(w.ctx, w.name,
		trace.WithTimestamp(w.start),
		trace.WithAttributes(attribute.String("saga.transfer_id", w.transferID)))
	span.End(trace.WithTimestamp(w.end))
}
