package agent_trace_agent

import (
	"context"
	"encoding/json"
	"testing"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

type scriptedLLM struct {
	responses []*llm.Response

	requests []llm.Request
}

func (s *scriptedLLM) Generate(
	_ context.Context,
	req llm.Request,
) (*llm.Response, error) {

	s.requests = append(
		s.requests,
		req,
	)

	response :=
		s.responses[0]

	s.responses =
		s.responses[1:]

	return response, nil
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

	registry, err :=
		tools.NewRegistry(
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

	secondMessages :=
		fake.requests[1].Messages

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
