package agent_trace_observability

import (
	"context"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// BaggageSpanProcessor copies Langfuse-related baggage values to every span.
// Langfuse recommends propagation for trace-level fields because filtering and
// aggregation increasingly operate over observations rather than only roots.
type BaggageSpanProcessor struct{}

func NewBaggageSpanProcessor() *BaggageSpanProcessor {
	return &BaggageSpanProcessor{}
}

func (p *BaggageSpanProcessor) OnStart(
	parent context.Context,
	span sdktrace.ReadWriteSpan,
) {

	for _, member := range baggage.FromContext(
		parent,
	).Members() {

		key := member.Key()

		if !shouldPropagateBaggageKey(
			key,
		) {
			continue
		}

		span.SetAttributes(
			attribute.String(
				key,
				member.Value(),
			),
		)
	}
}

func (p *BaggageSpanProcessor) OnEnd(
	sdktrace.ReadOnlySpan,
) {
}

func (p *BaggageSpanProcessor) Shutdown(
	context.Context,
) error {
	return nil
}

func (p *BaggageSpanProcessor) ForceFlush(
	context.Context,
) error {
	return nil
}

func shouldPropagateBaggageKey(
	key string,
) bool {

	return strings.HasPrefix(
		key,
		"langfuse.trace.",
	) ||
		key == "langfuse.user.id" ||
		key == "langfuse.session.id" ||
		key == attrEnvironment ||
		key == attrVersion ||
		key == "langfuse.release"
}
