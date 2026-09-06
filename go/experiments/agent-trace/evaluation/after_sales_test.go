package agent_trace_eval

import (
	"encoding/json"
	"testing"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
)

func TestEvaluateChecksBusinessOutcomeAndToolOrder(t *testing.T) {
	item := Case{
		ID: "after-sales-001",
		Expected: Expected{
			AnswerRules: []AnswerRule{
				{AnyOf: []string{"符合退货条件"}},
				{AnyOf: []string{"129"}},
			},
			RequiredTools: []ToolExpectation{
				{Name: "get_order_details", Arguments: map[string]any{"order_id": "A100"}},
				{Name: "get_product", Arguments: map[string]any{"product_id": "P100"}},
				{Name: "get_return_policy"},
				{Name: "calculator", Arguments: map[string]any{"expression": "129 * 1"}},
			},
			ToolOrder:    []string{"get_order_details", "get_product", "get_return_policy", "calculator"},
			MaxToolCalls: 4,
			MaxTurns:     5,
			ExpectedBusinessOutcome: &BusinessOutcome{
				Eligible:     true,
				RefundAmount: 129,
				ReasonCodes:  []string{"status_allowed", "within_return_window"},
			},
		},
	}
	result := &agent.RunResult{
		Answer: "订单 A100 符合退货条件，预计退款 129 元。",
		Turns:  5,
		ToolCalls: []agent.ToolCallRecord{
			{Name: "get_order_details", Arguments: json.RawMessage(`{"order_id":"A100"}`), Result: `{"found":true,"order_id":"A100","status":"shipped","product_id":"P100","quantity":1,"days_since_purchase":5}`},
			{Name: "get_product", Arguments: json.RawMessage(`{"product_id":"P100"}`), Result: `{"found":true,"product_id":"P100","name":"Basic Keyboard","price":129,"currency":"CNY","stock":10}`},
			{Name: "get_return_policy", Arguments: json.RawMessage(`{}`), Result: `{"return_window_days":30,"eligible_statuses":["shipped","delivered"],"refund_policy":"full_original_price"}`},
			{Name: "calculator", Arguments: json.RawMessage(`{"expression":"129 * 1"}`), Result: `{"expression":"129 * 1","result":129}`},
		},
	}

	got := Evaluate(item, result, nil)
	if !got.Passed {
		t.Fatalf("evaluation should pass: %+v", got)
	}
	if metricValue(got.Metrics, "business_outcome_correctness") != 1 {
		t.Fatalf("business outcome should pass: %+v", got.Metrics)
	}
	if metricValue(got.Metrics, "tool_order_accuracy") != 1 {
		t.Fatalf("tool order should pass: %+v", got.Metrics)
	}
}

func TestEvaluateRejectsIncorrectBusinessOutcome(t *testing.T) {
	item := Case{
		ID: "after-sales-001",
		Expected: Expected{
			RequiredTools: []ToolExpectation{
				{Name: "get_order_details", Arguments: map[string]any{"order_id": "A100"}},
				{Name: "get_product", Arguments: map[string]any{"product_id": "P100"}},
				{Name: "get_return_policy"},
				{Name: "calculator", Arguments: map[string]any{"expression": "129 * 1"}},
			},
			ExpectedBusinessOutcome: &BusinessOutcome{
				Eligible:     true,
				RefundAmount: 129,
				ReasonCodes:  []string{"status_allowed", "within_return_window"},
			},
		},
	}
	result := &agent.RunResult{ToolCalls: []agent.ToolCallRecord{
		{Name: "get_order_details", Arguments: json.RawMessage(`{"order_id":"A100"}`), Result: `{"found":true,"order_id":"A100","status":"shipped","product_id":"P100","quantity":1,"days_since_purchase":5}`},
		{Name: "get_product", Arguments: json.RawMessage(`{"product_id":"P100"}`), Result: `{"found":true,"product_id":"P100","price":129}`},
		{Name: "get_return_policy", Arguments: json.RawMessage(`{}`), Result: `{"return_window_days":30,"eligible_statuses":["shipped","delivered"],"refund_policy":"full_original_price"}`},
		{Name: "calculator", Arguments: json.RawMessage(`{"expression":"129 * 1"}`), Result: `{"expression":"129 * 1","result":128}`},
	}}

	got := Evaluate(item, result, nil)
	if got.Passed {
		t.Fatal("evaluation should fail for an incorrect refund amount")
	}
	if metricValue(got.Metrics, "business_outcome_correctness") != 0 {
		t.Fatalf("business outcome should fail: %+v", got.Metrics)
	}
}

func TestEvaluateRejectsUntrustedProductResults(t *testing.T) {
	item := Case{
		ID: "after-sales-001",
		Expected: Expected{
			RequiredTools: []ToolExpectation{
				{Name: "get_order_details", Arguments: map[string]any{"order_id": "A100"}},
				{Name: "get_product", Arguments: map[string]any{"product_id": "P100"}},
				{Name: "get_return_policy"},
				{Name: "calculator", Arguments: map[string]any{"expression": "129 * 1"}},
			},
			ExpectedBusinessOutcome: &BusinessOutcome{
				Eligible:     true,
				RefundAmount: 129,
			},
		},
	}
	commonCalls := []agent.ToolCallRecord{
		{Name: "get_order_details", Arguments: json.RawMessage(`{"order_id":"A100"}`), Result: `{"found":true,"order_id":"A100","status":"shipped","product_id":"P100","quantity":1,"days_since_purchase":5}`},
		{Name: "get_return_policy", Arguments: json.RawMessage(`{}`), Result: `{"return_window_days":30,"eligible_statuses":["shipped","delivered"],"refund_policy":"full_original_price"}`},
		{Name: "calculator", Arguments: json.RawMessage(`{"expression":"129 * 1"}`), Result: `{"expression":"129 * 1","result":129}`},
	}
	tests := []struct {
		name         string
		productCalls []agent.ToolCallRecord
	}{
		{
			name: "wrong call arguments",
			productCalls: []agent.ToolCallRecord{{
				Name:      "get_product",
				Arguments: json.RawMessage(`{"product_id":"P300"}`),
				Result:    `{"found":true,"product_id":"P300","price":129}`,
			}},
		},
		{
			name: "wrong returned identity",
			productCalls: []agent.ToolCallRecord{{
				Name:      "get_product",
				Arguments: json.RawMessage(`{"product_id":"P100"}`),
				Result:    `{"found":true,"product_id":"P300","price":129}`,
			}},
		},
		{
			name: "duplicate matching results",
			productCalls: []agent.ToolCallRecord{
				{
					Name:      "get_product",
					Arguments: json.RawMessage(`{"product_id":"P100"}`),
					Result:    `{"found":true,"product_id":"P100","price":129}`,
				},
				{
					Name:      "get_product",
					Arguments: json.RawMessage(`{"product_id":"P100"}`),
					Result:    `{"found":true,"product_id":"P100","price":130}`,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := append([]agent.ToolCallRecord{}, commonCalls[:1]...)
			calls = append(calls, test.productCalls...)
			calls = append(calls, commonCalls[1:]...)

			got := Evaluate(item, &agent.RunResult{ToolCalls: calls}, nil)
			if got.Passed {
				t.Fatal("evaluation should reject an untrusted product result")
			}
			if metricValue(got.Metrics, "business_outcome_correctness") != 0 {
				t.Fatalf("business outcome should fail: %+v", got.Metrics)
			}
		})
	}
}

func TestEvaluateRejectsCalculatorForIneligibleOrder(t *testing.T) {
	item := Case{
		ID: "after-sales-002",
		Expected: Expected{
			RequiredTools: []ToolExpectation{
				{Name: "get_order_details", Arguments: map[string]any{"order_id": "A104"}},
				{Name: "get_product", Arguments: map[string]any{"product_id": "P300"}},
				{Name: "get_return_policy"},
			},
			ExpectedBusinessOutcome: &BusinessOutcome{
				Eligible:     false,
				RefundAmount: 0,
				ReasonCodes:  []string{"status_allowed", "outside_return_window"},
			},
		},
	}
	result := &agent.RunResult{ToolCalls: []agent.ToolCallRecord{
		{Name: "get_order_details", Arguments: json.RawMessage(`{"order_id":"A104"}`), Result: `{"found":true,"order_id":"A104","status":"shipped","product_id":"P300","quantity":2,"days_since_purchase":45}`},
		{Name: "get_product", Arguments: json.RawMessage(`{"product_id":"P300"}`), Result: `{"found":true,"product_id":"P300","price":79}`},
		{Name: "get_return_policy", Arguments: json.RawMessage(`{}`), Result: `{"return_window_days":30,"eligible_statuses":["shipped","delivered"],"refund_policy":"full_original_price"}`},
		{Name: "calculator", Arguments: json.RawMessage(`{"expression":"79 * 2"}`), Result: `{"expression":"79 * 2","result":158}`},
	}}

	got := Evaluate(item, result, nil)
	if got.Passed {
		t.Fatal("evaluation should reject calculator for an ineligible order")
	}
	if metricValue(got.Metrics, "business_outcome_correctness") != 0 {
		t.Fatalf("business outcome should fail: %+v", got.Metrics)
	}
}

func TestEvaluateRejectsWrongToolOrder(t *testing.T) {
	item := Case{Expected: Expected{
		RequiredTools: []ToolExpectation{
			{Name: "get_order_details"},
			{Name: "get_product"},
			{Name: "get_return_policy"},
		},
		ToolOrder: []string{"get_order_details", "get_product", "get_return_policy"},
	}}
	result := &agent.RunResult{ToolCalls: []agent.ToolCallRecord{
		{Name: "get_product"},
		{Name: "get_order_details"},
		{Name: "get_return_policy"},
	}}

	got := Evaluate(item, result, nil)
	if got.Passed {
		t.Fatal("evaluation should fail for a wrong tool order")
	}
	if metricValue(got.Metrics, "tool_order_accuracy") != 0 {
		t.Fatalf("tool order should fail: %+v", got.Metrics)
	}
}
