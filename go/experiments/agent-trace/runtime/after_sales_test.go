package agent_trace_runtime

import (
	"context"
	"encoding/json"
	"testing"

	llm "harukizmoe/experiments/go/experiments/agent-trace/llm"
	tools "harukizmoe/experiments/go/experiments/agent-trace/tools"
)

func TestEngineRunsAfterSalesToolSequence(t *testing.T) {
	fake := &scriptedLLM{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID:        "call_order",
						Name:      "get_order_details",
						Arguments: json.RawMessage(`{"order_id":"A100"}`),
					}},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID:        "call_product",
						Name:      "get_product",
						Arguments: json.RawMessage(`{"product_id":"P100"}`),
					}},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID:        "call_policy",
						Name:      "get_return_policy",
						Arguments: json.RawMessage(`{}`),
					}},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID:        "call_calculator",
						Name:      "calculator",
						Arguments: json.RawMessage(`{"expression":"129 * 1"}`),
					}},
				},
				FinishReason: "tool_calls",
			},
			{
				Message: llm.Message{
					Role:    llm.RoleAssistant,
					Content: "订单 A100 符合退货条件，预计退款 129 元。",
				},
				FinishReason: "stop",
			},
		},
	}

	registry, err := tools.NewRegistry(
		tools.OrderDetailsTool{},
		tools.ProductTool{},
		tools.ReturnPolicyTool{},
		tools.CalculatorTool{},
	)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}

	engine, err := NewEngine(fake, registry, Config{MaxTurns: 5})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	result, _, err := engine.RunWithHistory(
		context.Background(),
		"请判断 A100 是否可以退货并估算退款金额。",
		[]llm.Message{{Role: llm.RoleSystem, Content: "test system prompt"}},
	)
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}

	wantNames := []string{
		"get_order_details",
		"get_product",
		"get_return_policy",
		"calculator",
	}
	if len(result.ToolCalls) != len(wantNames) {
		t.Fatalf("tool calls = %d, want %d", len(result.ToolCalls), len(wantNames))
	}
	for i, want := range wantNames {
		if result.ToolCalls[i].Name != want {
			t.Fatalf("tool call %d = %q, want %q", i, result.ToolCalls[i].Name, want)
		}
		if result.ToolCalls[i].Error != "" {
			t.Fatalf("tool call %d error = %q", i, result.ToolCalls[i].Error)
		}
	}
	if result.Answer != "订单 A100 符合退货条件，预计退款 129 元。" {
		t.Fatalf("answer = %q", result.Answer)
	}
}
