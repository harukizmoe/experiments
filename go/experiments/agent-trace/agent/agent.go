package agent_trace_agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

var ErrMaxTurnsExceeded = errors.New(
	"agent max turns exceeded",
)

const DefaultSystemPrompt = `You are a concise and reliable assistant with access to tools.
Use a relevant tool whenever the requested fact is provided by that tool instead of guessing.
Do not invent tool results.
Use tools only when they are helpful.
After receiving a tool result, answer based on that result.
If a tool reports that an item was not found, say that it was not found instead of making up data.`

type Config struct {
	SystemPrompt string
	MaxTurns     int
	Temperature  float64
	MaxTokens    int
	Debug        bool
	DebugWriter  io.Writer
}

type Agent struct {
	llm    llm.Client
	tools  *tools.Registry
	config Config
}

func New(
	client llm.Client,
	registry *tools.Registry,
	cfg Config,
) (*Agent, error) {

	if client == nil {
		return nil, errors.New(
			"LLM client is required",
		)
	}

	if registry == nil {
		return nil, errors.New(
			"tool registry is required",
		)
	}

	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 8
	}

	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = DefaultSystemPrompt
	}

	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}

	if cfg.DebugWriter == nil {
		cfg.DebugWriter = os.Stderr
	}

	return &Agent{
		llm:    client,
		tools:  registry,
		config: cfg,
	}, nil
}

func (a *Agent) Run(
	ctx context.Context,
	input string,
) (*RunResult, error) {

	started := time.Now()

	result := &RunResult{
		Input: input,
	}

	messages := []llm.Message{
		{
			Role:    llm.RoleSystem,
			Content: a.config.SystemPrompt,
		},
		{
			Role:    llm.RoleUser,
			Content: input,
		},
	}

	for turn := 1; turn <= a.config.MaxTurns; turn++ {

		a.debugf(
			"[agent] turn=%d\n",
			turn,
		)

		a.debugf(
			"[llm.request] messages=%d tools=%d\n",
			len(messages),
			len(a.tools.Definitions()),
		)

		response, err := a.llm.Generate(
			ctx,
			llm.Request{
				Messages:    messages,
				Tools:       a.tools.Definitions(),
				Temperature: a.config.Temperature,
				MaxTokens:   a.config.MaxTokens,
			},
		)

		if err != nil {
			result.Latency = time.Since(
				started,
			)

			return result, fmt.Errorf(
				"LLM turn %d: %w",
				turn,
				err,
			)
		}

		result.Turns++

		result.Usage.Add(
			response.Usage,
		)

		// 非常重要：
		// assistant tool_calls message 本身必须进入上下文。
		messages = append(
			messages,
			response.Message,
		)

		a.debugf(
			"[llm.response] finish_reason=%s content=%q tool_calls=%d tokens=%d\n",
			response.FinishReason,
			response.Message.Content,
			len(response.Message.ToolCalls),
			response.Usage.TotalTokens,
		)

		// 没有 tool call，说明 Agent Loop 结束。
		if len(response.Message.ToolCalls) == 0 {
			result.Answer =
				response.Message.Content

			result.FinalReason =
				response.FinishReason

			result.Latency =
				time.Since(started)

			return result, nil
		}

		for _, call := range response.Message.ToolCalls {

			if err := ctx.Err(); err != nil {
				result.Latency =
					time.Since(started)

				return result, err
			}

			a.debugf(
				"[tool.call] name=%s args=%s\n",
				call.Name,
				string(call.Arguments),
			)

			toolStarted := time.Now()

			toolResult, toolErr :=
				a.tools.Execute(
					ctx,
					call,
				)

			latency :=
				time.Since(toolStarted)

			record := ToolCallRecord{
				Turn: turn,

				ID: call.ID,

				Name: call.Name,

				Arguments: append(
					json.RawMessage(nil),
					call.Arguments...,
				),

				Result: toolResult,

				Latency: latency,
			}

			content := toolResult

			// Tool 出错时不直接结束整个 Agent。
			// 将 error 作为 tool result 回传给模型，
			// 允许模型自己恢复。
			if toolErr != nil {

				record.Error =
					toolErr.Error()

				content =
					toolErrorJSON(
						call.Name,
						toolErr,
					)

				a.debugf(
					"[tool.error] name=%s error=%q\n",
					call.Name,
					toolErr.Error(),
				)

			} else {

				a.debugf(
					"[tool.result] name=%s result=%s latency=%s\n",
					call.Name,
					toolResult,
					latency,
				)
			}

			result.ToolCalls = append(
				result.ToolCalls,
				record,
			)

			messages = append(
				messages,
				llm.Message{
					Role: llm.RoleTool,

					ToolCallID: call.ID,

					Content: content,
				},
			)
		}
	}

	result.FinalReason =
		"max_turns_exceeded"

	result.Latency =
		time.Since(started)

	return result, ErrMaxTurnsExceeded
}

func (a *Agent) debugf(
	format string,
	args ...any,
) {

	if !a.config.Debug {
		return
	}

	_, _ = fmt.Fprintf(
		a.config.DebugWriter,
		format,
		args...,
	)
}

func toolErrorJSON(
	name string,
	err error,
) string {

	payload := map[string]any{
		"ok": false,

		"tool": name,

		"error": err.Error(),
	}

	b, marshalErr :=
		json.Marshal(payload)

	if marshalErr != nil {
		return fmt.Sprintf(
			`{"ok":false,"tool":%q,"error":%q}`,
			name,
			err.Error(),
		)
	}

	return string(b)
}
