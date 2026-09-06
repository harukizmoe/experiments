package agent_trace_eval

import (
	"context"
	"encoding/json"
	"testing"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
)

type scriptedConversationRunner struct {
	results []*agent.RunResult
	inputs  []string
}

func (r *scriptedConversationRunner) Run(_ context.Context, input string) (*agent.RunResult, error) {
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
	first := &agent.RunResult{
		Input:  item.Input,
		Answer: "请提供订单号。",
		Turns:  1,
	}
	second := &agent.RunResult{
		Input:  item.FollowUps[0],
		Answer: "订单 A100 的状态是 shipped。",
		Turns:  2,
		ToolCalls: []agent.ToolCallRecord{{
			Name:      "lookup_order",
			Arguments: json.RawMessage(`{"order_id":"A100"}`),
		}},
	}

	got := EvaluateConversation(item, []*agent.RunResult{first, second}, nil)
	if !got.Passed {
		t.Fatalf("conversation should pass: %+v", got)
	}
	if len(got.TurnEvaluations) != 2 || !got.TurnEvaluations[0].Passed || !got.TurnEvaluations[1].Passed {
		t.Fatalf("turn evaluations = %+v", got.TurnEvaluations)
	}
	if metricValue(got.Metrics, "conversation_turn_correctness") != 1 {
		t.Fatalf("conversation metric = %+v", got.Metrics)
	}

	first.ToolCalls = []agent.ToolCallRecord{{
		Name:      "lookup_order",
		Arguments: json.RawMessage(`{"order_id":"A100"}`),
	}}
	got = EvaluateConversation(item, []*agent.RunResult{first, second}, nil)
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
		results: []*agent.RunResult{
			{
				Input:  item.Input,
				Answer: "请提供订单号。",
				Turns:  1,
			},
			{
				Input:  item.FollowUps[0],
				Answer: "订单 A100 的状态是 shipped。",
				Turns:  2,
				ToolCalls: []agent.ToolCallRecord{{
					Name:      "lookup_order",
					Arguments: json.RawMessage(`{"order_id":"A100"}`),
				}},
			},
		},
	}

	results, err := (Runner{
		AgentForCase: func(Case) (agent.Runner, error) { return scripted, nil },
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
