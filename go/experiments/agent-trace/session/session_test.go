package agent_trace_session

import (
	"context"
	"errors"
	"reflect"
	"testing"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"
)

type scriptedTurn struct {
	result *runtime.RunResult
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
) (*runtime.RunResult, []llm.Message, error) {
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
				result: &runtime.RunResult{
					Input:  "我想退货",
					Answer: "请提供订单号。",
				},
				next: firstHistory,
			},
			{
				result: &runtime.RunResult{
					Input: "订单号是 A100",
				},
				err: context.Canceled,
			},
			{
				result: &runtime.RunResult{
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

func TestSessionOwnsNestedInitialHistory(t *testing.T) {
	initial := []llm.Message{{
		Role: llm.RoleAssistant,
		ToolCalls: []llm.ToolCall{{
			ID:        "call_1",
			Name:      "lookup_order",
			Arguments: []byte(`{"order_id":"A100"}`),
		}},
	}}
	executor := &scriptedExecutor{
		steps: []scriptedTurn{{
			result: &runtime.RunResult{Input: "next"},
			next:   []llm.Message{{Role: llm.RoleAssistant, Content: "done"}},
		}},
	}
	session, err := New(executor, initial)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	initial[0].ToolCalls[0].Arguments[0] = 'X'
	if _, err := session.Run(context.Background(), "next"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	got := string(executor.calls[0].history[0].ToolCalls[0].Arguments)
	if got != `{"order_id":"A100"}` {
		t.Fatalf("session history shares nested arguments: %q", got)
	}
}

func TestSessionPromptAndReset(t *testing.T) {
	initial := []llm.Message{{
		Role:    llm.RoleSystem,
		Content: "system",
	}}
	executor := &scriptedExecutor{
		steps: []scriptedTurn{
			{
				result: &runtime.RunResult{
					Input:  "first",
					Answer: "first answer",
				},
				next: []llm.Message{
					initial[0],
					{Role: llm.RoleUser, Content: "first"},
					{Role: llm.RoleAssistant, Content: "first answer"},
				},
			},
			{
				result: &runtime.RunResult{
					Input:  "second",
					Answer: "second answer",
				},
				next: []llm.Message{
					initial[0],
					{Role: llm.RoleUser, Content: "second"},
					{Role: llm.RoleAssistant, Content: "second answer"},
				},
			},
		},
	}
	session, err := New(executor, initial)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	answer, err := session.Prompt(context.Background(), "first")
	if err != nil {
		t.Fatalf("Prompt() error = %v", err)
	}
	if answer != "first answer" {
		t.Fatalf("Prompt() = %q, want %q", answer, "first answer")
	}

	session.Reset()
	if _, err := session.Run(context.Background(), "second"); err != nil {
		t.Fatalf("Run() after Reset error = %v", err)
	}
	if !reflect.DeepEqual(executor.calls[1].history, initial) {
		t.Fatalf("history after Reset = %+v, want %+v", executor.calls[1].history, initial)
	}
}
