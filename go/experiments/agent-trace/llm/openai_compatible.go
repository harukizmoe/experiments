package agent_trace_llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type OpenAICompatibleConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

type OpenAICompatibleClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewOpenAICompatibleClient(cfg OpenAICompatibleConfig) (*OpenAICompatibleClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, errors.New("base URL is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("model is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}

	return &OpenAICompatibleClient{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		model:   strings.TrimSpace(cfg.Model),
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}, nil
}

func (c *OpenAICompatibleClient) Generate(ctx context.Context, req Request) (*Response, error) {
	payload := chatCompletionRequest{
		Model:       c.model,
		Messages:    make([]chatMessage, 0, len(req.Messages)),
		Temperature: req.Temperature,
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}

	for _, message := range req.Messages {
		payload.Messages = append(payload.Messages, toAPIMessage(message))
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]chatTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, chatTool{
				Type: "function",
				Function: chatFunctionDefinition{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Parameters,
				},
			})
		}
		payload.ToolChoice = "auto"
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completion request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create chat completion request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("chat completion request: %w", err)
	}
	defer httpResp.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read chat completion response: %w", err)
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return nil, decodeAPIError(httpResp.StatusCode, responseBody)
	}

	var response chatCompletionResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf(
			"decode chat completion response: %w; body=%s",
			err,
			truncate(string(responseBody), 1000),
		)
	}

	if len(response.Choices) == 0 {
		return nil, errors.New("chat completion response contains no choices")
	}

	choice := response.Choices[0]

	message := Message{
		Role:    RoleAssistant,
		Content: valueOrEmpty(choice.Message.Content),
	}

	for _, call := range choice.Message.ToolCalls {
		if call.Type != "" && call.Type != "function" {
			continue
		}

		message.ToolCalls = append(message.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
		})
	}

	return &Response{
		Message: message,
		Usage: Usage{
			InputTokens:  response.Usage.PromptTokens,
			OutputTokens: response.Usage.CompletionTokens,
			TotalTokens:  response.Usage.TotalTokens,
		},
		FinishReason: choice.FinishReason,
		Model:        response.Model,
	}, nil
}

func toAPIMessage(message Message) chatMessage {
	apiMessage := chatMessage{
		Role:       string(message.Role),
		ToolCallID: message.ToolCallID,
	}

	if message.Content != "" ||
		message.Role != RoleAssistant ||
		len(message.ToolCalls) == 0 {

		content := message.Content
		apiMessage.Content = &content
	}

	if len(message.ToolCalls) > 0 {
		apiMessage.ToolCalls = make([]chatToolCall, 0, len(message.ToolCalls))

		for _, call := range message.ToolCalls {
			apiMessage.ToolCalls = append(
				apiMessage.ToolCalls,
				chatToolCall{
					ID:   call.ID,
					Type: "function",
					Function: chatToolCallFunction{
						Name:      call.Name,
						Arguments: string(call.Arguments),
					},
				},
			)
		}
	}

	return apiMessage
}

func decodeAPIError(statusCode int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}

	if err := json.Unmarshal(body, &envelope); err == nil &&
		envelope.Error.Message != "" {

		return fmt.Errorf(
			"LLM API error: status=%d type=%s code=%v message=%s",
			statusCode,
			envelope.Error.Type,
			envelope.Error.Code,
			envelope.Error.Message,
		)
	}

	return fmt.Errorf(
		"LLM API error: status=%d body=%s",
		statusCode,
		truncate(string(body), 2000),
	)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Tools       []chatTool    `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string                 `json:"type"`
	Function chatFunctionDefinition `json:"function"`
}

type chatFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type chatToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionResponse struct {
	Model string `json:"model"`

	Choices []struct {
		Message struct {
			Role      string         `json:"role"`
			Content   *string        `json:"content"`
			ToolCalls []chatToolCall `json:"tool_calls"`
		} `json:"message"`

		FinishReason string `json:"finish_reason"`
	} `json:"choices"`

	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}
