package agent_trace_observability

import (
	"context"
	"strconv"

	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// TracedRunner 为完成的 Runtime Runner 调用创建根观测 Span，并记录结果或错误。
type TracedRunner struct {
	next           runtime.Runner
	tracer         trace.Tracer
	traceContext   TraceContext
	captureContent bool
}

// WrapRunner 在 Runtime Runner 外部添加完成执行级观测，不改变 Runner 的同步契约。
func WrapRunner(
	next runtime.Runner,
	tracer trace.Tracer,
	traceContext TraceContext,
	captureContent bool,
) runtime.Runner {
	return &TracedRunner{
		next:           next,
		tracer:         tracer,
		traceContext:   traceContext,
		captureContent: captureContent,
	}
}

func (r *TracedRunner) Run(
	ctx context.Context,
	input string,
) (*runtime.RunResult, error) {
	propagated, err := WithTraceContext(
		ctx,
		r.traceContext,
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

	if r.traceContext.TraceName != "" {
		attrs = append(
			attrs,
			attribute.String(
				attrTraceName,
				r.traceContext.TraceName,
			),
		)
	}

	if r.traceContext.Environment != "" {
		attrs = append(
			attrs,
			attribute.String(
				attrEnvironment,
				r.traceContext.Environment,
			),
		)
	}

	if r.traceContext.Version != "" {
		attrs = append(
			attrs,
			attribute.String(
				attrVersion,
				r.traceContext.Version,
			),
		)
	}

	for key, value := range r.traceContext.Metadata {
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

	if r.captureContent {
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

	ctx, span := r.tracer.Start(
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

	result, runErr := r.next.Run(
		ctx,
		input,
	)

	if result != nil {
		if spanContext.TraceID().
			IsValid() {

			result.TraceID = spanContext.TraceID().
				String()
		}

		if spanContext.SpanID().
			IsValid() {

			result.RootObservationID = spanContext.SpanID().
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

		if r.captureContent {
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
