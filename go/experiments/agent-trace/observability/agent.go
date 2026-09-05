package agent_trace_observability

import (
	"context"
	"strconv"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TracedAgent struct {
	next           agent.Runner
	tracer         trace.Tracer
	traceContext   TraceContext
	captureContent bool
}

func WrapAgent(
	next agent.Runner,
	tracer trace.Tracer,
	traceContext TraceContext,
	captureContent bool,
) agent.Runner {

	return &TracedAgent{
		next:           next,
		tracer:         tracer,
		traceContext:   traceContext,
		captureContent: captureContent,
	}
}

func (a *TracedAgent) Run(
	ctx context.Context,
	input string,
) (*agent.RunResult, error) {

	propagated, err :=
		WithTraceContext(
			ctx,
			a.traceContext,
		)

	if err == nil {
		ctx = propagated
	}

	attrs := []attribute.KeyValue{
		attribute.String(
			attrObservationType,
			"agent",
		),
	}

	if a.traceContext.TraceName != "" {
		attrs = append(
			attrs,
			attribute.String(
				attrTraceName,
				a.traceContext.TraceName,
			),
		)
	}

	if a.traceContext.Environment != "" {
		attrs = append(
			attrs,
			attribute.String(
				attrEnvironment,
				a.traceContext.Environment,
			),
		)
	}

	if a.traceContext.Version != "" {
		attrs = append(
			attrs,
			attribute.String(
				attrVersion,
				a.traceContext.Version,
			),
		)
	}

	for key, value := range a.traceContext.Metadata {

		if value != "" {
			attrs = append(
				attrs,
				attribute.String(
					"langfuse.trace.metadata."+
						key,
					value,
				),
			)
		}
	}

	if a.captureContent {
		attrs = append(
			attrs,
			attribute.String(
				attrObservationInput,
				jsonString(
					map[string]string{
						"query": input,
					},
				),
			),
		)
	}

	if err != nil {
		attrs = append(
			attrs,
			attribute.String(
				"langfuse.observation.metadata.baggage_error",
				err.Error(),
			),
		)
	}

	ctx, span := a.tracer.Start(
		ctx,
		"agent.run",
		trace.WithSpanKind(
			trace.SpanKindInternal,
		),
		trace.WithAttributes(
			attrs...,
		),
	)

	defer span.End()

	spanContext := span.SpanContext()

	result, runErr :=
		a.next.Run(
			ctx,
			input,
		)

	if result != nil {
		if spanContext.TraceID().
			IsValid() {

			result.TraceID =
				spanContext.TraceID().
					String()
		}

		if spanContext.SpanID().
			IsValid() {

			result.RootObservationID =
				spanContext.SpanID().
					String()
		}

		span.SetAttributes(
			attribute.Int(
				"agent.turns",
				result.Turns,
			),

			attribute.Int(
				"agent.tool_calls",
				len(result.ToolCalls),
			),

			attribute.Int(
				"agent.total_tokens",
				result.Usage.TotalTokens,
			),

			attribute.String(
				"langfuse.observation.metadata.turns",
				strconv.Itoa(
					result.Turns,
				),
			),

			attribute.String(
				"langfuse.observation.metadata.tool_calls",
				strconv.Itoa(
					len(result.ToolCalls),
				),
			),

			attribute.String(
				"langfuse.observation.metadata.total_tokens",
				strconv.Itoa(
					result.Usage.TotalTokens,
				),
			),

			attribute.String(
				"langfuse.observation.metadata.final_reason",
				result.FinalReason,
			),
		)

		if a.captureContent {
			span.SetAttributes(
				attribute.String(
					attrObservationOutput,
					jsonString(
						map[string]any{
							"answer": result.Answer,

							"turns": result.Turns,

							"tool_calls": result.ToolCalls,

							"usage": result.Usage,

							"final_reason": result.FinalReason,
						},
					),
				),
			)
		}
	}

	if runErr != nil {
		span.RecordError(
			runErr,
		)

		span.SetStatus(
			codes.Error,
			runErr.Error(),
		)

		span.SetAttributes(
			attribute.String(
				attrObservationStatus,
				runErr.Error(),
			),
		)
	}

	return result, runErr
}
