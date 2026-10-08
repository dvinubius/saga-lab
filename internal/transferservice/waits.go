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

func recordWait(ctx context.Context, name, transferID string, start, end time.Time) {
	_, span := tracer.Start(ctx, name,
		trace.WithTimestamp(start),
		trace.WithAttributes(attribute.String("saga.transfer_id", transferID)))
	span.End(trace.WithTimestamp(end))
}
