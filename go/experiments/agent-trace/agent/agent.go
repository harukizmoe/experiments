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

// Config 控制 Agent Loop 的执行上限、模型参数和调试输出。
type Config struct {
	MaxTurns    int
	Temperature float64
	MaxTokens   int	
	Debug       bool
	DebugWriter io.Writer
}

// Agent 负责一次用户轮次内的 LLM 与工具协作，不持有会话历史。
type Agent struct {
	llm    llm.Client
	tools  *tools.Registry
	config Config
}

// New 创建一个不持有对话状态的 Agent 执行器。
func New(client llm.Client, registry *tools.Registry, cfg Config) (*Agent, error) {
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

	return &Agent{
		llm:    client,
		tools:  registry,
		config: cfg,
	}, nil
}

// RunWithHistory 执行一个用户轮次，并返回成功后的下一份消息历史。
// history 由调用方拥有；成功时 nextHistory 的所有权转移给调用方，失败时不提交本轮消息。
func (a *Agent) RunWithHistory(ctx context.Context, input string, history []llm.Message) (*RunResult, []llm.Message, error) {
	localHistory := make(
		[]llm.Message,
		len(history),
		len(history)+1,
	)
	copy(localHistory, history)

	result, err := a.run(ctx, input, &localHistory)
	if err != nil {
		return result, nil, err
	}

	return result, localHistory, nil
}

func (a *Agent) run(ctx context.Context, input string, history *[]llm.Message) (result *RunResult, runErr error) {
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

	for turn := 1; turn <= a.config.MaxTurns; turn++ {
		// 每轮开始前响应调用方取消，避免在已结束的请求上继续调用 LLM。
		if err := ctx.Err(); err != nil {
			result.Latency = time.Since(started)
			return result, err
		}

		a.debugf("[agent] turn=%d\n", turn)

		a.debugf("[llm.request] messages=%d tools=%d\n", len(messages), len(a.tools.Definitions()))

		response, err := a.llm.Generate(ctx, llm.Request{
			Messages:    messages,
			Tools:       a.tools.Definitions(),
			Temperature: a.config.Temperature,
			MaxTokens:   a.config.MaxTokens,
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

		a.debugf(
			"[llm.response] finish_reason=%s content=%q tool_calls=%d tokens=%d\n",
			response.FinishReason,
			response.Message.Content,
			len(response.Message.ToolCalls),
			response.Usage.TotalTokens,
		)

		// 没有 tool call，说明本轮 Agent Loop 结束。
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

			a.debugf("[tool.call] name=%s args=%s\n", call.Name, string(call.Arguments))

			toolStarted := time.Now()
			toolResult, toolErr := a.tools.Execute(ctx, call)
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

			// Tool 出错时不直接结束整个 Agent。
			// 将 error 作为 tool result 回传给模型，
			// 允许模型自己恢复。
			if toolErr != nil {
				record.Error = toolErr.Error()
				content = toolErrorJSON(call.Name, toolErr)
				a.debugf("[tool.error] name=%s error=%q\n", call.Name, toolErr.Error())
			} else {
				a.debugf("[tool.result] name=%s result=%s latency=%s\n", call.Name, toolResult, latency)
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

			messages = append(messages, llm.Message{
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
