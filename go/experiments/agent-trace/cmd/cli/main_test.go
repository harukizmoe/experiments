package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	llm "harukizmoe/experiments/go/experiments/agent-trace/llm"
	observability "harukizmoe/experiments/go/experiments/agent-trace/observability"
	policy "harukizmoe/experiments/go/experiments/agent-trace/policy"
	runtime "harukizmoe/experiments/go/experiments/agent-trace/runtime"
)

type capturedTool struct {
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type capturedRequest struct {
	Messages []llm.Message  `json:"messages"`
	Tools    []capturedTool `json:"tools"`
}

func TestNewRunnerCreatesIsolatedSessions(t *testing.T) {
	var (
		mu       sync.Mutex
		requests []capturedRequest
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request capturedRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		requests = append(requests, request)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model",
			"choices": []any{
				map[string]any{
					"message": map[string]any{
						"role":    "assistant",
						"content": "ok",
					},
					"finish_reason": "stop",
				},
			},
			"usage": map[string]any{
				"prompt_tokens":     1,
				"completion_tokens": 1,
				"total_tokens":      2,
			},
		})
	}))
	defer server.Close()

	app, err := New(context.Background(), Config{
		LLM: llm.OpenAICompatibleConfig{
			BaseURL: server.URL,
			Model:   "test-model",
		},
		Runtime: runtime.Config{
			MaxTurns: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first, err := app.NewRunner(observability.TraceContext{})
	if err != nil {
		t.Fatalf("NewRunner(first) error = %v", err)
	}
	if _, err := first.Run(context.Background(), "first input"); err != nil {
		t.Fatalf("first.Run() error = %v", err)
	}

	second, err := app.NewRunner(observability.TraceContext{})
	if err != nil {
		t.Fatalf("NewRunner(second) error = %v", err)
	}
	if _, err := second.Run(context.Background(), "second input"); err != nil {
		t.Fatalf("second.Run() error = %v", err)
	}

	mu.Lock()
	gotRequests := append([]capturedRequest(nil), requests...)
	mu.Unlock()

	if len(gotRequests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(gotRequests))
	}

	if got := gotRequests[0].Messages; len(got) != 2 ||
		got[0].Role != llm.RoleSystem || got[0].Content != policy.DefaultSystemPrompt ||
		got[1].Role != llm.RoleUser || got[1].Content != "first input" {
		t.Fatalf("first request messages = %#v, want injected policy and first input", got)
	}

	if got := gotRequests[1].Messages; len(got) != 2 ||
		got[0].Role != llm.RoleSystem || got[0].Content != policy.DefaultSystemPrompt ||
		got[1].Role != llm.RoleUser || got[1].Content != "second input" {
		t.Fatalf("second request messages = %#v, want injected policy and second input only", got)
	}

	wantTools := []string{
		"calculator",
		"get_order_details",
		"get_product",
		"get_return_policy",
		"lookup_order",
	}
	if len(gotRequests[0].Tools) != len(wantTools) {
		t.Fatalf("registered tools = %d, want %d", len(gotRequests[0].Tools), len(wantTools))
	}
	for i, want := range wantTools {
		if got := gotRequests[0].Tools[i].Function.Name; got != want {
			t.Fatalf("tool %d = %q, want %q", i, got, want)
		}
	}
}
