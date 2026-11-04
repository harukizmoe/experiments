package agent_trace_observability

import (
	"context"
	"encoding/json"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TracedTool struct {
	next tools.Tool

	tracer trace.Tracer

	captureContent bool
}

func WrapTool(
	next tools.Tool,
	tracer trace.Tracer,
	captureContent bool,
) tools.Tool {

	return &TracedTool{
		next: next,

		tracer: tracer,

		captureContent: captureContent,
	}
}

func (t *TracedTool) Definition() llm.ToolDefinition {
	return t.next.Definition()
}

func (t *TracedTool) Execute(
	ctx context.Context,
	arguments json.RawMessage,
) (string, error) {

	definition :=
		t.next.Definition()

	attrs := []attribute.KeyValue{
		attribute.String(
			attrObservationType,
			"tool",
		),

		attribute.String(
			"langfuse.observation.metadata.tool_name",
			definition.Name,
		),
	}

	if t.captureContent {
		attrs = append(
			attrs,
			attribute.String(
				attrObservationInput,
				jsonOrString(
					string(arguments),
				),
			),
		)
	}

	ctx, span := t.tracer.Start(
		ctx,
		"tool."+definition.Name,

		trace.WithSpanKind(
			trace.SpanKindInternal,
		),

		trace.WithAttributes(
			attrs...,
		),
	)

	defer span.End()

	result, err :=
		t.next.Execute(
			ctx,
			arguments,
		)

	if err != nil {
		span.RecordError(err)

		span.SetStatus(
			codes.Error,
			err.Error(),
		)

		span.SetAttributes(
			attribute.String(
				attrObservationStatus,
				err.Error(),
			),
		)
	}

	if t.captureContent &&
		result != "" {

		span.SetAttributes(
			attribute.String(
				attrObservationOutput,
				jsonOrString(
					result,
				),
			),
		)
	}

	return result, err
}
