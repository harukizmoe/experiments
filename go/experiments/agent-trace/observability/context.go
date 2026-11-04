package agent_trace_observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/baggage"
)

type TraceContext struct {
	TraceName   string
	Environment string
	Version     string
	Metadata    map[string]string
}

func WithTraceContext(ctx context.Context, values TraceContext) (context.Context, error) {

	bag := baggage.FromContext(ctx)

	entries := map[string]string{
		attrTraceName:   values.TraceName,
		attrEnvironment: values.Environment,
		attrVersion:     values.Version,
	}

	for key, value := range values.Metadata {
		entries["langfuse.trace.metadata."+key] = value
	}

	for key, value := range entries {
		if value == "" {
			continue
		}
		member, err := baggage.NewMember(key, value)

		if err != nil {
			return ctx, fmt.Errorf("create baggage member %s: %w", key, err)
		}
		bag, err = bag.SetMember(member)

		if err != nil {
			return ctx, fmt.Errorf("set baggage member %s: %w", key, err)
		}
	}
	return baggage.ContextWithBaggage(ctx, bag), nil
}
