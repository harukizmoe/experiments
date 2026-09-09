package agent_test

import (
	"context"
	"testing"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

type finalResponseClient struct{}

func (finalResponseClient) Generate(
	context.Context,
	llm.Request,
) (*llm.Response, error) {
	return &llm.Response{
		Message: llm.Message{
			Role:    llm.RoleAssistant,
			Content: "done",
		},
		FinishReason: "stop",
	}, nil
}

func TestNewRejectsNilEngine(t *testing.T) {
	if _, err := agent.New(nil); err == nil {
		t.Fatal("New() error = nil, want an error")
	}
}

func TestAgentDelegatesExecutionWithoutOwningHistory(t *testing.T) {
	registry, err := tools.NewRegistry()
	if err != nil {
		t.Fatalf("tools.NewRegistry() error = %v", err)
	}
	engine, err := runtime.NewEngine(finalResponseClient{}, registry, runtime.Config{})
	if err != nil {
		t.Fatalf("runtime.NewEngine() error = %v", err)
	}
	executor, err := agent.New(engine)
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	history := []llm.Message{{Role: llm.RoleSystem, Content: "system"}}
	result, nextHistory, err := executor.RunWithHistory(
		context.Background(),
		"hello",
		history,
	)
	if err != nil {
		t.Fatalf("RunWithHistory() error = %v", err)
	}
	if result.Answer != "done" {
		t.Fatalf("RunWithHistory() answer = %q, want %q", result.Answer, "done")
	}
	if len(history) != 1 {
		t.Fatalf("input history length = %d, want 1", len(history))
	}
	if len(nextHistory) != 3 {
		t.Fatalf("next history length = %d, want 3", len(nextHistory))
	}
	if nextHistory[1].Role != llm.RoleUser || nextHistory[2].Role != llm.RoleAssistant {
		t.Fatalf("next history roles = %+v, want user then assistant", nextHistory)
	}
}
