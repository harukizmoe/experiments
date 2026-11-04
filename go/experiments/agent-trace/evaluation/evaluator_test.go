package agent_trace_eval

import (
	"encoding/json"
	"testing"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
)

func TestEvaluateAllowsOptionalCalculator(t *testing.T) {
	item := Case{
		ID:    "multi-001",
		Input: "买3个P100一共多少钱？",
		Expected: Expected{
			AnswerRules: []AnswerRule{{AnyOf: []string{"387"}}},
			RequiredTools: []ToolExpectation{{
				Name:      "get_product",
				Arguments: map[string]any{"product_id": "P100"},
			}},
			OptionalTools: []string{"calculator"},
			MaxToolCalls:  2,
			MaxTurns:      3,
		},
	}
	result := &agent.RunResult{
		Answer: "总价是 387 元。",
		Turns:  2,
		ToolCalls: []agent.ToolCallRecord{{
			Name:      "get_product",
			Arguments: json.RawMessage(`{"product_id":"p100"}`),
		}},
	}

	got := Evaluate(item, result, nil)
	if !got.Passed {
		t.Fatalf("evaluation should pass: %+v", got)
	}
	if metricValue(got.Metrics, "tool_precision") != 1 || metricValue(got.Metrics, "tool_recall") != 1 {
		t.Fatalf("unexpected precision/recall: %+v", got.Metrics)
	}
}

func TestEvaluatePenalizesWrongArgumentsAndExtraCall(t *testing.T) {
	item := Case{
		ID: "order-001",
		Expected: Expected{
			RequiredTools: []ToolExpectation{{
				Name:      "lookup_order",
				Arguments: map[string]any{"order_id": "A102"},
			}},
			MaxToolCalls: 1,
		},
	}
	result := &agent.RunResult{
		Turns: 2,
		ToolCalls: []agent.ToolCallRecord{
			{Name: "lookup_order", Arguments: json.RawMessage(`{"order_id":"A100"}`)},
			{Name: "lookup_order", Arguments: json.RawMessage(`{"order_id":"A102"}`)},
		},
	}

	got := Evaluate(item, result, nil)
	if got.Passed {
		t.Fatalf("evaluation should fail")
	}
	if metricValue(got.Metrics, "tool_selection_accuracy") != 0 {
		t.Fatalf("selection should fail because max_tool_calls was exceeded")
	}
	if metricValue(got.Metrics, "unnecessary_tool_calls") != 1 {
		t.Fatalf("expected one unnecessary tool call: %+v", got.Metrics)
	}
}

func TestCalculatorExpressionIgnoresWhitespace(t *testing.T) {
	if !argumentsMatch(
		"calculator",
		map[string]any{"expression": "144 / 12"},
		json.RawMessage(`{"expression":"144/12"}`),
	) {
		t.Fatalf("calculator expression should ignore whitespace")
	}
}

func TestPhase3NotFoundAnswerVariantPasses(t *testing.T) {
	dataset, err := LoadDataset("../evaldata/cases.jsonl")
	if err != nil {
		t.Fatalf("LoadDataset() error = %v", err)
	}

	var item Case
	for _, candidate := range dataset.Cases {
		if candidate.ID == "edge-002" {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		t.Fatal("edge-002 case not found")
	}

	result := &agent.RunResult{
		Answer: "没有找到商品 P999，因此无法查询价格。",
		Turns:  2,
		ToolCalls: []agent.ToolCallRecord{{
			Name:      "get_product",
			Arguments: json.RawMessage(`{"product_id":"P999"}`),
		}},
	}
	if got := Evaluate(item, result, nil); !got.Passed {
		t.Fatalf("edge-002 natural not-found answer should pass: %+v", got)
	}
}

func TestPhase3VerifiesExplicitOrderRequest(t *testing.T) {
	dataset, err := LoadDataset("../evaldata/cases.jsonl")
	if err != nil {
		t.Fatalf("LoadDataset() error = %v", err)
	}

	var item Case
	for _, candidate := range dataset.Cases {
		if candidate.ID == "edge-001" {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		t.Fatal("edge-001 case not found")
	}

	result := &agent.RunResult{
		Answer: "A100 的状态是：已发货。",
		Turns:  2,
		ToolCalls: []agent.ToolCallRecord{{
			Name:      "lookup_order",
			Arguments: json.RawMessage(`{"order_id":"A100"}`),
		}},
	}
	if got := Evaluate(item, result, nil); !got.Passed {
		t.Fatalf("edge-001 verified answer should pass: %+v", got)
	}
}
