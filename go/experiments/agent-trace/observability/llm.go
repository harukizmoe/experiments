package agent_trace_observability

import (
	"context"
	"fmt"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TracedLLM struct {
	next llm.Client

	tracer trace.Tracer

	configuredModel string

	captureContent bool
}

func WrapLLM(
	next llm.Client,
	tracer trace.Tracer,
	configuredModel string,
	captureContent bool,
) llm.Client {

	return &TracedLLM{
		next: next,

		tracer: tracer,

		configuredModel: configuredModel,

		captureContent: captureContent,
	}
}

func (c *TracedLLM) Generate(
	ctx context.Context,
	req llm.Request,
) (*llm.Response, error) {

	attrs := []attribute.KeyValue{
		attribute.String(
			attrObservationType,
			"generation",
		),

		attribute.String(
			attrObservationModelName,
			c.configuredModel,
		),

		attribute.String(
			attrGenAIOperationName,
			"chat",
		),

		attribute.String(
			attrGenAIRequestModel,
			c.configuredModel,
		),

		attribute.String(
			attrObservationModelParam,
			jsonString(
				map[string]any{
					"temperature": req.Temperature,

					"max_tokens": req.MaxTokens,
				},
			),
		),
	}

	if c.captureContent {
		attrs = append(
			attrs,
			attribute.String(
				attrObservationInput,
				jsonString(
					map[string]any{
						"messages": req.Messages,

						"tools": req.Tools,
					},
				),
			),
		)
	}

	ctx, span := c.tracer.Start(
		ctx,
		"llm.chat.completion",

		trace.WithSpanKind(
			trace.SpanKindClient,
		),

		trace.WithAttributes(
			attrs...,
		),
	)

	defer span.End()

	response, err :=
		c.next.Generate(
			ctx,
			req,
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

		return response, err
	}

	model := response.Model

	if model == "" {
		model =
			c.configuredModel
	}

	span.SetAttributes(
		attribute.String(
			attrObservationModelName,
			model,
		),

		attribute.String(
			attrGenAIResponseModel,
			model,
		),

		attribute.Int(
			attrGenAIInputTokens,
			response.Usage.InputTokens,
		),

		attribute.Int(
			attrGenAIOutputTokens,
			response.Usage.OutputTokens,
		),

		attribute.String(
			attrObservationUsage,
			jsonString(
				map[string]int{
					"input": response.Usage.InputTokens,

					"output": response.Usage.OutputTokens,

					"total": response.Usage.TotalTokens,
				},
			),
		),

		attribute.String(
			"langfuse.observation.metadata.finish_reason",
			response.FinishReason,
		),
	)

	if c.captureContent {
		span.SetAttributes(
			attribute.String(
				attrObservationOutput,
				jsonString(
					map[string]any{
						"message": response.Message,

						"finish_reason": response.FinishReason,
					},
				),
			),
		)
	}

	if response.Message.Role !=
		llm.RoleAssistant {

		warning := fmt.Sprintf(
			"unexpected LLM response role: %s",
			response.Message.Role,
		)

		span.SetAttributes(
			attribute.String(
				"langfuse.observation.metadata.warning",
				warning,
			),
		)
	}

	return response, nil
}
