package agent_trace_runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	llm "harukizmoe/experiments/go/experiments/agent-trace/llm"
	tools "harukizmoe/experiments/go/experiments/agent-trace/tools"
)

// ErrMaxTurnsExceeded 表示 Runtime 在允许的最大轮次内未得到最终回答。
var ErrMaxTurnsExceeded = errors.New(
	"runtime max turns exceeded",
)

// Config 控制 Runtime Loop 的执行上限、模型参数和调试输出。
type Config struct {
	MaxTurns    int
	Temperature float64
	MaxTokens   int
	Debug       bool
	DebugWriter io.Writer
}

// Engine 负责一次用户轮次内的 LLM 与工具协作，不持有会话历史。
type Engine struct {
	llm    llm.Client
	tools  *tools.Registry
	config Config
}

// NewEngine 创建一个不持有对话状态的 Runtime 执行器。
func NewEngine(client llm.Client, registry *tools.Registry, cfg Config) (*Engine, error) {
	if client == nil {
		return nil, errors.New("LLM client is required")
	}

	if registry == nil {
		return nil, errors.New("tool registry is required")
	}

	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 8
	}

	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 2048
	}

	if cfg.DebugWriter == nil {
		cfg.DebugWriter = os.Stderr
	}

	return &Engine{
		llm:    client,
		tools:  registry,
		config: cfg,
	}, nil
}

// RunWithHistory 执行一个用户轮次，并返回成功后的下一份消息历史。
// history 由调用方拥有；成功时 nextHistory 的所有权转移给调用方，失败时不提交本轮消息。
func (e *Engine) RunWithHistory(ctx context.Context, input string, history []llm.Message) (*RunResult, []llm.Message, error) {
	localHistory := llm.CloneMessages(history)

	result, err := e.run(ctx, input, &localHistory)
	if err != nil {
		return result, nil, err
	}

	return result, localHistory, nil
}

func (e *Engine) run(ctx context.Context, input string, history *[]llm.Message) (result *RunResult, runErr error) {
	started := time.Now()

	result = &RunResult{
		Input: input,
	}

	// 本轮只在局部历史上执行；失败时保持调用方传入的 history 不变。
	// 成功时才把完整的 assistant/tool 消息提交给 RunWithHistory。
	messages := append(*history, llm.Message{Role: llm.RoleUser, Content: input})
	defer func() {
		if runErr == nil {
			*history = messages
		}
	}()

	for turn := 1; turn <= e.config.MaxTurns; turn++ {
		// 每轮开始前响应调用方取消，避免在已结束的请求上继续调用 LLM。
		if err := ctx.Err(); err != nil {
			result.Latency = time.Since(started)
			return result, err
		}

		e.debugf("[runtime] turn=%d\n", turn)

		e.debugf("[llm.request] messages=%d tools=%d\n", len(messages), len(e.tools.Definitions()))

		response, err := e.llm.Generate(
			ctx, llm.Request{
				Messages:    messages,
				Tools:       e.tools.Definitions(),
				Temperature: e.config.Temperature,
				MaxTokens:   e.config.MaxTokens,
			},
		)
		// LLM 可能在调用方取消后才返回；丢弃本轮响应，避免继续规划。
		if ctxErr := ctx.Err(); ctxErr != nil {
			result.Latency = time.Since(started)
			return result, ctxErr
		}
		if err != nil {
			result.Latency = time.Since(started)
			return result, fmt.Errorf("LLM turn %d: %w", turn, err)
		}

		result.Turns++
		result.Usage.Add(response.Usage)

		// 非常重要：
		// assistant tool_calls message 本身必须进入上下文。
		messages = append(messages, response.Message)

		e.debugf(
			"[llm.response] finish_reason=%s content=%q tool_calls=%d tokens=%d\n",
			response.FinishReason,
			response.Message.Content,
			len(response.Message.ToolCalls),
			response.Usage.TotalTokens,
		)

		// 没有 tool call，说明本轮 Runtime 执行结束。
		if len(response.Message.ToolCalls) == 0 {
			result.Answer = response.Message.Content
			result.FinalReason = response.FinishReason
			result.Latency = time.Since(started)
			return result, nil
		}

		for _, call := range response.Message.ToolCalls {
			if err := ctx.Err(); err != nil {
				result.Latency = time.Since(started)
				return result, err
			}

			e.debugf("[tool.call] name=%s args=%s\n", call.Name, string(call.Arguments))

			toolStarted := time.Now()
			toolResult, toolErr := e.tools.Execute(ctx, call)
			latency := time.Since(toolStarted)
			record := ToolCallRecord{
				Turn:      turn,
				ID:        call.ID,
				Name:      call.Name,
				Arguments: append(json.RawMessage(nil), call.Arguments...),
				Result:    toolResult,
				Latency:   latency,
			}

			content := toolResult

			// Tool 出错时不直接结束整个 Runtime。
			// 将 error 作为 tool result 回传给模型，
			// 允许模型自己恢复。
			if toolErr != nil {
				record.Error = toolErr.Error()
				content = toolErrorJSON(call.Name, toolErr)
				e.debugf("[tool.error] name=%s error=%q\n", call.Name, toolErr.Error())
			} else {
				e.debugf("[tool.result] name=%s result=%s latency=%s\n", call.Name, toolResult, latency)
			}

			result.ToolCalls = append(
				result.ToolCalls,
				record,
			)
			// 工具调用结束后再次检查取消；已记录的调用不再回传给 LLM。
			if ctxErr := ctx.Err(); ctxErr != nil {
				result.Latency = time.Since(started)
				return result, ctxErr
			}
			if toolErr != nil && (errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded)) {
				result.Latency = time.Since(started)
				return result, toolErr
			}

			messages = append(
				messages, llm.Message{
					Role:       llm.RoleTool,
					ToolCallID: call.ID,
					Content:    content,
				},
			)
		}
	}

	result.FinalReason = "max_turns_exceeded"
	result.Latency = time.Since(started)

	return result, ErrMaxTurnsExceeded
}

func (e *Engine) debugf(
	format string,
	args ...any,
) {
	if !e.config.Debug {
		return
	}

	_, _ = fmt.Fprintf(
		e.config.DebugWriter,
		format,
		args...,
	)
}

func toolErrorJSON(name string, err error) string {
	payload := map[string]any{
		"ok":    false,
		"tool":  name,
		"error": err.Error(),
	}

	b, marshalErr := json.Marshal(payload)

	if marshalErr != nil {
		return fmt.Sprintf(`{"ok":false,"tool":%q,"error":%q}`, name, err.Error())
	}

	return string(b)
}
