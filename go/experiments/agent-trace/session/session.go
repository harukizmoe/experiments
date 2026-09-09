package agent_trace_session

import (
	"context"
	"fmt"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"
)

// TurnExecutor 执行一个用户轮次，并返回成功后的完整消息历史。
// 实现不得修改传入的 history；成功时 nextHistory 的所有权转移给 Session，失败时不会提交。
type TurnExecutor interface {
	RunWithHistory(
		ctx context.Context,
		input string,
		history []llm.Message,
	) (*runtime.RunResult, []llm.Message, error)
}

// Session 持有一段对话的消息历史，并把每个用户轮次交给 TurnExecutor。
// Session 不是并发安全的；同一会话必须由调用方串行使用。
type Session struct {
	executor       TurnExecutor
	initialHistory []llm.Message
	messages       []llm.Message
}

var _ runtime.Runner = (*Session)(nil)

// New 创建一个有状态的 Agent 会话。
//
// executor 通常是 runtime.Engine；initialHistory 通常由程序入口从 policy
// 生成。Session 会复制初始历史，因此调用方可以安全复用自己的切片。
// Session 不会创建 Provider 或工具，也不发起网络请求。
func New(
	executor TurnExecutor,
	initialHistory []llm.Message,
) (*Session, error) {
	if executor == nil {
		return nil, fmt.Errorf("turn executor is required")
	}

	initial := llm.CloneMessages(initialHistory)

	return &Session{
		executor:       executor,
		initialHistory: initial,
		messages:       llm.CloneMessages(initial),
	}, nil
}

// Run 处理一个用户轮次；只有执行成功时才提交新的消息历史。
// 失败或取消不会污染下一次 Run 看到的会话状态。
func (s *Session) Run(
	ctx context.Context,
	input string,
) (*runtime.RunResult, error) {
	result, nextHistory, err := s.executor.RunWithHistory(
		ctx,
		input,
		s.messages,
	)
	if err != nil {
		return result, err
	}

	s.messages = nextHistory
	return result, nil
}

// Prompt 执行一个用户轮次并只返回最终回答文本。
// 需要工具调用记录、Token 用量或延迟时使用 Run。
func (s *Session) Prompt(
	ctx context.Context,
	prompt string,
) (string, error) {
	result, err := s.Run(ctx, prompt)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.Answer, nil
}

// Reset 丢弃当前对话历史，并恢复创建会话时的初始历史。
// Provider、工具和 system message 配置保持不变。
func (s *Session) Reset() {
	s.messages = llm.CloneMessages(s.initialHistory)
}
