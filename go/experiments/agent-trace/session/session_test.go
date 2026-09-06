package agent_trace_session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type scriptedTurn struct {
	result *agent.RunResult
	next   []llm.Message
	err    error
}

type scriptedExecutor struct {
	steps []scriptedTurn
	calls []struct {
		input   string
		history []llm.Message
	}
}

func (e *scriptedExecutor) RunWithHistory(
	_ context.Context,
	input string,
	history []llm.Message,
) (*agent.RunResult, []llm.Message, error) {
	e.calls = append(e.calls, struct {
		input   string
		history []llm.Message
	}{
		input:   input,
		history: append([]llm.Message(nil), history...),
	})

	step := e.steps[0]
	e.steps = e.steps[1:]
	return step.result, append([]llm.Message(nil), step.next...), step.err
}

func TestSessionPreservesHistoryAcrossUserTurns(t *testing.T) {
	initial := []llm.Message{{
		Role:    llm.RoleSystem,
		Content: "system",
	}}
	firstHistory := append(
		append([]llm.Message(nil), initial...),
		llm.Message{Role: llm.RoleUser, Content: "我想退货"},
		llm.Message{Role: llm.RoleAssistant, Content: "请提供订单号。"},
	)
	thirdHistory := append(
		append([]llm.Message(nil), firstHistory...),
		llm.Message{Role: llm.RoleUser, Content: "订单号是 A100"},
		llm.Message{Role: llm.RoleAssistant, Content: "订单 A100 可以退货。"},
	)
	executor := &scriptedExecutor{
		steps: []scriptedTurn{
			{
				result: &agent.RunResult{
					Input:  "我想退货",
					Answer: "请提供订单号。",
				},
				next: firstHistory,
			},
			{
				result: &agent.RunResult{
					Input: "订单号是 A100",
				},
				err: context.Canceled,
			},
			{
				result: &agent.RunResult{
					Input:  "订单号是 A100",
					Answer: "订单 A100 可以退货。",
				},
				next: thirdHistory,
			},
		},
	}

	session, err := New(executor, initial)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := session.Run(context.Background(), "我想退货")
	if err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if first.Answer != "请提供订单号。" {
		t.Fatalf("first result = %+v", first)
	}

	_, err = session.Run(context.Background(), "订单号是 A100")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("second Run() error = %v, want context.Canceled", err)
	}

	third, err := session.Run(context.Background(), "订单号是 A100")
	if err != nil {
		t.Fatalf("third Run() error = %v", err)
	}
	if third.Answer != "订单 A100 可以退货。" {
		t.Fatalf("third result = %+v", third)
	}

	if len(executor.calls) != 3 {
		t.Fatalf("executor calls = %d, want 3", len(executor.calls))
	}
	if !reflect.DeepEqual(executor.calls[0].history, initial) {
		t.Fatalf("first history = %+v, want %+v", executor.calls[0].history, initial)
	}
	if !reflect.DeepEqual(executor.calls[1].history, firstHistory) {
		t.Fatalf("second history = %+v, want %+v", executor.calls[1].history, firstHistory)
	}
	if !reflect.DeepEqual(executor.calls[2].history, firstHistory) {
		t.Fatalf("failed turn polluted history: %+v", executor.calls[2].history)
	}
}

func TestNewRejectsNilExecutor(t *testing.T) {
	if _, err := New(nil, nil); err == nil {
		t.Fatal("New() error = nil, want error")
	}
}
