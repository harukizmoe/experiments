package agent_trace_agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

type scriptedLLM struct {
	responses []*llm.Response

	requests       []llm.Request
	beforeGenerate func()
}

func (s *scriptedLLM) Generate(
	_ context.Context,
	req llm.Request,
) (*llm.Response, error) {
	s.requests = append(
		s.requests,
		req,
	)

	response := s.responses[0]

	s.responses = s.responses[1:]
	if s.beforeGenerate != nil {
		s.beforeGenerate()
	}

	return response, nil
}

type cancelingTool struct {
	cancel context.CancelFunc
}

func (cancelingTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "cancel_tool",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (tool cancelingTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	tool.cancel()
	return "", ctx.Err()
}

func TestAgentToolLoop(t *testing.T) {
	fake := &scriptedLLM{
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

				Usage: llm.Usage{
					InputTokens: 10,

					OutputTokens: 5,

					TotalTokens: 15,
				},
			},

			{
				Message: llm.Message{
					Role: llm.RoleAssistant,

					Content: "A102 has been refunded.",
				},

				FinishReason: "stop",

				Usage: llm.Usage{
					InputTokens: 20,

					OutputTokens: 6,

					TotalTokens: 26,
				},
			},
		},
	}

	registry, err := tools.NewRegistry(
		tools.OrderTool{},
	)
	if err != nil {
		t.Fatalf(
			"NewRegistry() error = %v",
			err,
		)
	}

	a, err := New(
		fake,
		registry,
		Config{
			MaxTurns: 4,

			Temperature: 0,

			MaxTokens: 512,
		},
	)
	if err != nil {
		t.Fatalf(
			"New() error = %v",
			err,
		)
	}

	result, err := a.Run(
		context.Background(),
		"A102 refunded?",
	)
	if err != nil {
		t.Fatalf(
			"Run() error = %v",
			err,
		)
	}

	if result.Answer !=
		"A102 has been refunded." {

		t.Fatalf(
			"answer = %q",
			result.Answer,
		)
	}

	if result.Turns != 2 ||
		len(result.ToolCalls) != 1 {

		t.Fatalf(
			"unexpected result: %+v",
			result,
		)
	}

	if result.Usage.TotalTokens != 41 {
		t.Fatalf(
			"total tokens = %d",
			result.Usage.TotalTokens,
		)
	}

	if len(fake.requests) != 2 {
		t.Fatalf(
			"requests = %d",
			len(fake.requests),
		)
	}

	secondMessages := fake.requests[1].Messages

	if len(secondMessages) != 4 {
		t.Fatalf(
			"second request messages = %d",
			len(secondMessages),
		)
	}

	if secondMessages[2].Role !=
		llm.RoleAssistant ||
		len(secondMessages[2].ToolCalls) != 1 {

		t.Fatalf(
			"assistant tool-call message missing: %+v",
			secondMessages[2],
		)
	}

	if secondMessages[3].Role !=
		llm.RoleTool ||
		secondMessages[3].ToolCallID !=
			"call_1" {

		t.Fatalf(
			"tool result message missing: %+v",
			secondMessages[3],
		)
	}
}

func TestAgentStopsBeforeLLMWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	fake := &scriptedLLM{
		responses: []*llm.Response{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "must not be returned",
			},
			FinishReason: "stop",
		}},
	}
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	a, err := New(fake, registry, Config{MaxTurns: 1})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, runErr := a.Run(ctx, "cancelled request")
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", runErr)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("LLM requests = %d, want 0", len(fake.requests))
	}
	if result.Answer != "" {
		t.Fatalf("answer = %q, want empty", result.Answer)
	}
}

func TestAgentStopsWhenContextCanceledAfterLLMResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &scriptedLLM{
		beforeGenerate: cancel,
		responses: []*llm.Response{{
			Message: llm.Message{
				Role:    llm.RoleAssistant,
				Content: "must not be accepted",
			},
			FinishReason: "stop",
		}},
	}
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	a, err := New(fake, registry, Config{MaxTurns: 1})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, runErr := a.Run(ctx, "cancel after response")
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", runErr)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("LLM requests = %d, want 1", len(fake.requests))
	}
	if result.Answer != "" {
		t.Fatalf("answer = %q, want empty", result.Answer)
	}
}

func TestAgentStopsAfterToolContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := &scriptedLLM{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID:        "cancel_call",
						Name:      "cancel_tool",
						Arguments: json.RawMessage(`{}`),
					}},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: "must not be requested",
				},
				FinishReason: "stop",
			},
		},
	}
	registry, err := tools.NewRegistry(cancelingTool{cancel: cancel})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	a, err := New(fake, registry, Config{MaxTurns: 2})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, runErr := a.Run(ctx, "cancel during tool")
	if !errors.Is(runErr, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled", runErr)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("LLM requests = %d, want 1", len(fake.requests))
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Error == "" {
		t.Fatalf("tool calls = %+v, want one recorded cancellation", result.ToolCalls)
	}
}
