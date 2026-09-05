package agent_trace_llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOpenAICompatibleClientToolCall(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/chat/completions" {
				t.Fatalf("path = %s", r.URL.Path)
			}

			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Fatalf("Authorization = %q", got)
			}

			var request chatCompletionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			if len(request.Tools) != 1 ||
				request.Tools[0].Function.Name != "lookup_order" {

				t.Fatalf("unexpected tools: %+v", request.Tools)
			}

			w.Header().Set("Content-Type", "application/json")

			_, _ = w.Write([]byte(`{
				"model":"test-model",
				"choices":[{
					"message":{
						"role":"assistant",
						"content":null,
						"tool_calls":[{
							"id":"call_1",
							"type":"function",
							"function":{
								"name":"lookup_order",
								"arguments":"{\"order_id\":\"A102\"}"
							}
						}]
					},
					"finish_reason":"tool_calls"
				}],
				"usage":{
					"prompt_tokens":10,
					"completion_tokens":5,
					"total_tokens":15
				}
			}`))
		}),
	)
	defer server.Close()

	client, err := NewOpenAICompatibleClient(
		OpenAICompatibleConfig{
			BaseURL: server.URL + "/v1",
			APIKey:  "test-key",
			Model:   "test-model",
			Timeout: time.Second,
		},
	)
	if err != nil {
		t.Fatalf(
			"NewOpenAICompatibleClient() error = %v",
			err,
		)
	}

	response, err := client.Generate(
		context.Background(),
		Request{
			Messages: []Message{
				{
					Role:    RoleUser,
					Content: "A102 refunded?",
				},
			},
			Tools: []ToolDefinition{
				{
					Name: "lookup_order",
					Parameters: map[string]any{
						"type": "object",
					},
				},
			},
			Temperature: 0,
			MaxTokens:   100,
		},
	)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf(
			"tool calls = %d",
			len(response.Message.ToolCalls),
		)
	}

	call := response.Message.ToolCalls[0]

	if call.Name != "lookup_order" ||
		string(call.Arguments) != `{"order_id":"A102"}` {

		t.Fatalf("unexpected tool call: %+v", call)
	}

	if response.Usage.TotalTokens != 15 {
		t.Fatalf(
			"total tokens = %d",
			response.Usage.TotalTokens,
		)
	}
}
