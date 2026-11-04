package agent_trace_observability

import (
	"context"
	"encoding/json"
	"testing"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type scriptedLLM struct {
	responses []*llm.Response
}

func (s *scriptedLLM) Generate(
	_ context.Context,
	_ llm.Request,
) (*llm.Response, error) {

	response :=
		s.responses[0]

	s.responses =
		s.responses[1:]

	return response, nil
}

func TestAgentLLMToolTraceHierarchy(
	t *testing.T,
) {

	recorder := tracetest.NewSpanRecorder()

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(
			NewBaggageSpanProcessor(),
		),

		sdktrace.WithSpanProcessor(
			recorder,
		),
	)

	defer func() {
		_ = tp.Shutdown(
			context.Background(),
		)
	}()

	tracer := tp.Tracer(
		instrumentationName,
	)

	fakeLLM := &scriptedLLM{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,

					ToolCalls: []llm.ToolCall{
						{
							ID: "call_1",

							Name: "lookup_order",

							Arguments: json.RawMessage(
								`{"order_id":"A102"}`,
							),
						},
					},
				},

				FinishReason: "tool_calls",

				Model: "test-model",

				Usage: llm.Usage{
					InputTokens: 10,

					OutputTokens: 4,

					TotalTokens: 14,
				},
			},

			{
				Message: llm.Message{
					Role: llm.RoleAssistant,

					Content: "A102 has been refunded.",
				},

				FinishReason: "stop",

				Model: "test-model",

				Usage: llm.Usage{
					InputTokens: 18,

					OutputTokens: 6,

					TotalTokens: 24,
				},
			},
		},
	}

	wrappedLLM := WrapLLM(
		fakeLLM,
		tracer,
		"test-model",
		true,
	)

	registry, err :=
		tools.NewRegistry(
			WrapTool(
				tools.OrderTool{},
				tracer,
				true,
			),
		)

	if err != nil {
		t.Fatalf(
			"NewRegistry() error = %v",
			err,
		)
	}

	baseAgent, err :=
		agent.New(
			wrappedLLM,
			registry,
			agent.Config{
				MaxTurns: 4,
			},
		)

	if err != nil {
		t.Fatalf(
			"agent.New() error = %v",
			err,
		)
	}

	runner := WrapAgent(
		baseAgent,
		tracer,
		TraceContext{
			TraceName: "agent-chat",

			Environment: "test",

			Version: "phase-2",

			Metadata: map[string]string{
				"phase": "2",
			},
		},
		true,
	)

	result, err := runner.Run(
		context.Background(),
		"A102 refunded?",
	)

	if err != nil {
		t.Fatalf(
			"Run() error = %v",
			err,
		)
	}

	if result.TraceID == "" ||
		result.RootObservationID == "" {

		t.Fatalf(
			"trace identifiers were not written to result: %+v",
			result,
		)
	}

	spans := recorder.Ended()

	if len(spans) != 4 {
		t.Fatalf(
			"ended spans = %d, want 4",
			len(spans),
		)
	}

	byName :=
		make(
			map[string][]sdktrace.ReadOnlySpan,
		)

	for _, span := range spans {
		byName[span.Name()] = append(
			byName[span.Name()],
			span,
		)

		if span.SpanContext().
			TraceID().
			String() !=
			result.TraceID {

			t.Fatalf(
				"span %s is on a different trace",
				span.Name(),
			)
		}
	}

	root :=
		byName["agent.run"][0]

	if root.Parent().
		IsValid() {

		t.Fatalf(
			"agent.run unexpectedly has a parent",
		)
	}

	if len(
		byName["llm.chat.completion"],
	) != 2 {

		t.Fatalf(
			"LLM span count = %d, want 2",
			len(byName["llm.chat.completion"]),
		)
	}

	if len(
		byName["tool.lookup_order"],
	) != 1 {

		t.Fatalf(
			"tool span count = %d, want 1",
			len(byName["tool.lookup_order"]),
		)
	}

	children := append(
		byName["llm.chat.completion"],
		byName["tool.lookup_order"]...,
	)

	for _, child := range children {
		if child.Parent().
			SpanID() !=
			root.SpanContext().
				SpanID() {

			t.Fatalf(
				"span %s parent = %s, want root %s",
				child.Name(),
				child.Parent().SpanID(),
				root.SpanContext().SpanID(),
			)
		}
	}

	if got := stringAttribute(
		root,
		attrTraceName,
	); got != "agent-chat" {

		t.Fatalf(
			"trace name attribute = %q",
			got,
		)
	}

	if got := stringAttribute(
		byName["tool.lookup_order"][0],
		attrTraceName,
	); got != "agent-chat" {

		t.Fatalf(
			"propagated trace name on tool = %q",
			got,
		)
	}
}

func TestTracesEndpoint(
	t *testing.T,
) {

	tests := []struct {
		base     string
		explicit string
		want     string
	}{
		{
			"https://cloud.langfuse.com",
			"",
			"https://cloud.langfuse.com/api/public/otel/v1/traces",
		},
		{
			"https://cloud.langfuse.com/api/public/otel",
			"",
			"https://cloud.langfuse.com/api/public/otel/v1/traces",
		},
		{
			"",
			"https://example.com/custom/v1/traces",
			"https://example.com/custom/v1/traces",
		},
	}

	for _, tt := range tests {
		got, err :=
			tracesEndpoint(
				tt.base,
				tt.explicit,
			)

		if err != nil {
			t.Fatalf(
				"tracesEndpoint() error = %v",
				err,
			)
		}

		if got != tt.want {
			t.Fatalf(
				"tracesEndpoint() = %q, want %q",
				got,
				tt.want,
			)
		}
	}
}

func stringAttribute(
	span sdktrace.ReadOnlySpan,
	key string,
) string {

	for _, kv := range span.Attributes() {

		if string(kv.Key) ==
			key {

			return kv.Value.
				AsString()
		}
	}

	return ""
}
