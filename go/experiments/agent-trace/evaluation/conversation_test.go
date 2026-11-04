package agent_trace_eval

import (
	"context"
	"encoding/json"
	"testing"

	runtime "harukizmoe/experiments/go/experiments/agent-trace/runtime"
)

type scriptedConversationRunner struct {
	results []*runtime.RunResult
	inputs  []string
}

func (r *scriptedConversationRunner) Run(_ context.Context, input string) (*runtime.RunResult, error) {
	r.inputs = append(r.inputs, input)
	result := r.results[len(r.inputs)-1]
	return result, nil
}

func clarificationCase() Case {
	return Case{
		ID:        "conversation-001",
		Input:     "我想退货",
		FollowUps: []string{"订单号是 A100"},
		Expected: Expected{
			AnswerRules: []AnswerRule{{AnyOf: []string{"shipped"}}},
			RequiredTools: []ToolExpectation{{
				Name:      "lookup_order",
				Arguments: map[string]any{"order_id": "A100"},
			}},
			ToolOrder:    []string{"lookup_order"},
			MaxToolCalls: 1,
			MaxTurns:     4,
			TurnExpectations: []TurnExpectation{
				{
					AnswerRules: []AnswerRule{{AnyOf: []string{"订单号"}}},
					MaxTurns:    1,
				},
				{
					AnswerRules: []AnswerRule{{AnyOf: []string{"shipped"}}},
					RequiredTools: []ToolExpectation{{
						Name:      "lookup_order",
						Arguments: map[string]any{"order_id": "A100"},
					}},
					ToolOrder:    []string{"lookup_order"},
					MaxToolCalls: 1,
					MaxTurns:     2,
				},
			},
		},
	}
}

func TestEvaluateConversationChecksTurnSpecificContract(t *testing.T) {
	item := clarificationCase()
	first := &runtime.RunResult{
		Input:  item.Input,
		Answer: "请提供订单号。",
		Turns:  1,
	}
	second := &runtime.RunResult{
		Input:  item.FollowUps[0],
		Answer: "订单 A100 的状态是 shipped。",
		Turns:  2,
		ToolCalls: []runtime.ToolCallRecord{{
			Name:      "lookup_order",
			Arguments: json.RawMessage(`{"order_id":"A100"}`),
		}},
	}

	got := EvaluateConversation(item, []*runtime.RunResult{first, second}, nil)
	if !got.Passed {
		t.Fatalf("conversation should pass: %+v", got)
	}
	if len(got.TurnEvaluations) != 2 || !got.TurnEvaluations[0].Passed || !got.TurnEvaluations[1].Passed {
		t.Fatalf("turn evaluations = %+v", got.TurnEvaluations)
	}
	if metricValue(got.Metrics, "conversation_turn_correctness") != 1 {
		t.Fatalf("conversation metric = %+v", got.Metrics)
	}

	first.ToolCalls = []runtime.ToolCallRecord{{
		Name:      "lookup_order",
		Arguments: json.RawMessage(`{"order_id":"A100"}`),
	}}
	got = EvaluateConversation(item, []*runtime.RunResult{first, second}, nil)
	if got.Passed {
		t.Fatal("conversation should fail when the clarification turn calls a tool")
	}
	if got.TurnEvaluations[0].Passed {
		t.Fatalf("first turn should fail: %+v", got.TurnEvaluations[0])
	}
}

func TestRunnerUsesOneRunnerForAllConversationTurns(t *testing.T) {
	item := clarificationCase()
	scripted := &scriptedConversationRunner{
		results: []*runtime.RunResult{
			{
				Input:  item.Input,
				Answer: "请提供订单号。",
				Turns:  1,
			},
			{
				Input:  item.FollowUps[0],
				Answer: "订单 A100 的状态是 shipped。",
				Turns:  2,
				ToolCalls: []runtime.ToolCallRecord{{
					Name:      "lookup_order",
					Arguments: json.RawMessage(`{"order_id":"A100"}`),
				}},
			},
		},
	}

	results, err := (Runner{
		RunnerForCase: func(Case) (runtime.Runner, error) { return scripted, nil },
	}).Run(context.Background(), Dataset{Cases: []Case{item}})
	if err != nil {
		t.Fatalf("Runner.Run() error = %v", err)
	}
	if len(results) != 1 || !results[0].Evaluation.Passed {
		t.Fatalf("case results = %+v", results)
	}
	if len(scripted.inputs) != 2 || scripted.inputs[0] != item.Input || scripted.inputs[1] != item.FollowUps[0] {
		t.Fatalf("inputs = %v", scripted.inputs)
	}
	if len(results[0].TurnResults) != 2 {
		t.Fatalf("turn results = %+v", results[0].TurnResults)
	}
	if results[0].AgentResult == nil || results[0].AgentResult.Answer != "订单 A100 的状态是 shipped。" ||
		results[0].AgentResult.Turns != 3 || len(results[0].AgentResult.ToolCalls) != 1 {
		t.Fatalf("aggregate result = %+v", results[0].AgentResult)
	}
}

func TestCorrectionConversationUsesTurnContracts(t *testing.T) {
	dataset, err := LoadDataset("../evaldata/cases.jsonl")
	if err != nil {
		t.Fatalf("LoadDataset() error = %v", err)
	}

	var item Case
	for _, candidate := range dataset.Cases {
		if candidate.ID == "after-sales-004" {
			item = candidate
			break
		}
	}
	if item.ID == "" {
		t.Fatal("after-sales-004 case not found")
	}
	if len(item.FollowUps) != 1 || len(item.Expected.TurnExpectations) != 2 {
		t.Fatalf("correction case turns = follow_ups=%d turn_expectations=%d", len(item.FollowUps), len(item.Expected.TurnExpectations))
	}

	first := &runtime.RunResult{
		Input:  item.Input,
		Answer: "未找到订单 A999。请提供正确的订单号。",
		Turns:  2,
		ToolCalls: []runtime.ToolCallRecord{{
			Name:      "get_order_details",
			Arguments: json.RawMessage(`{"order_id":"A999"}`),
			Result:    `{"found":false,"order_id":"A999"}`,
		}},
	}
	second := &runtime.RunResult{
		Input:  item.FollowUps[0],
		Answer: "订单 A100 符合退货条件，预计退款 129 元，尚未执行退款。",
		Turns:  4,
		ToolCalls: []runtime.ToolCallRecord{
			{
				Name:      "get_order_details",
				Arguments: json.RawMessage(`{"order_id":"A100"}`),
				Result:    `{"days_since_purchase":5,"found":true,"order_id":"A100","product_id":"P100","quantity":1,"status":"shipped"}`,
			},
			{
				Name:      "get_product",
				Arguments: json.RawMessage(`{"product_id":"P100"}`),
				Result:    `{"found":true,"price":129,"product_id":"P100"}`,
			},
			{
				Name:   "get_return_policy",
				Result: `{"eligible_statuses":["shipped","delivered"],"refund_policy":"full_original_price","return_window_days":30}`,
			},
			{
				Name:      "calculator",
				Arguments: json.RawMessage(`{"expression":"129 * 1"}`),
				Result:    `{"expression":"129 * 1","result":129}`,
			},
		},
	}

	got := EvaluateConversation(item, []*runtime.RunResult{first, second}, nil)
	if !got.Passed {
		t.Fatalf("correction conversation should pass: %+v", got)
	}
	if len(got.TurnEvaluations) != 2 || !got.TurnEvaluations[0].Passed || !got.TurnEvaluations[1].Passed {
		t.Fatalf("turn evaluations = %+v", got.TurnEvaluations)
	}
	if metricValue(got.Metrics, "tool_precision") != 1 || metricValue(got.Metrics, "unnecessary_tool_calls") != 0 {
		t.Fatalf("correction tool metrics = %+v", got.Metrics)
	}

	wrongSecond := *second
	wrongSecond.ToolCalls = append([]runtime.ToolCallRecord(nil), second.ToolCalls...)
	wrongSecond.ToolCalls[0].Arguments = json.RawMessage(`{"order_id":"A999"}`)
	got = EvaluateConversation(item, []*runtime.RunResult{first, &wrongSecond}, nil)
	if got.Passed {
		t.Fatal("conversation should fail when the corrected order is not queried")
	}
}
