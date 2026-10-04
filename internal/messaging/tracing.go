package messaging

import (
	"context"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-amqp/v3/pkg/amqp"
	"github.com/ThreeDotsLabs/watermill/message"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/dvinubius/saga-lab/internal/messaging")

type attemptIDKey struct{}

func AttemptID(ctx context.Context) string {
	id, _ := ctx.Value(attemptIDKey{}).(string)
	return id
}

func AttemptLogger(ctx context.Context, logger *slog.Logger) *slog.Logger {
	return logger.With("attempt_id", AttemptID(ctx))
}

func send(ctx context.Context, publisher message.Publisher, topic string, msg *message.Message) error {
	ctx, span := tracer.Start(ctx, "send "+topic,
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(messageAttributes("send", topic, msg)...))
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.Metadata))
	err := publisher.Publish(topic, msg)
	endSpan(span, err)
	return err
}

func traceHandling(h message.HandlerFunc) message.HandlerFunc {
	return func(msg *message.Message) ([]*message.Message, error) {
		topic := message.SubscribeTopicFromCtx(msg.Context())
		attemptID := watermill.NewUUID()
		ctx := otel.GetTextMapPropagator().Extract(msg.Context(), propagation.MapCarrier(msg.Metadata))
		ctx = context.WithValue(ctx, attemptIDKey{}, attemptID)
		ctx, span := tracer.Start(ctx, "process "+topic,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(messageAttributes("process", topic, msg)...),
			trace.WithAttributes(
				attribute.String("saga.attempt_id", attemptID),
				attribute.Bool("messaging.rabbitmq.message.redelivered", amqp.IsMessageRedelivered(msg)),
			))
		msg.SetContext(ctx)
		produced, err := h(msg)
		endSpan(span, err)
		return produced, err
	}
}

func messageAttributes(operation, topic string, msg *message.Message) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("messaging.system", "rabbitmq"),
		attribute.String("messaging.operation.type", operation),
		attribute.String("messaging.destination.name", topic),
		attribute.String("messaging.message.id", msg.UUID),
		attribute.String("saga.transfer_id", msg.Metadata.Get(transferIDKey)),
	}
	if causationID := CausationID(msg); causationID != "" {
		attributes = append(attributes, attribute.String("saga.causation_id", causationID))
	}
	return attributes
}

func endSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}
