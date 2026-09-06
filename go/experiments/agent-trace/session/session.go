package agent_trace_session

import (
	"context"
	"fmt"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

// TurnExecutor 执行一个用户轮次，并返回成功后的完整消息历史。
// 实现不得修改传入的 history；成功时 nextHistory 的所有权转移给 Session，失败时不会提交。
type TurnExecutor interface {
	RunWithHistory(
		ctx context.Context,
		input string,
		history []llm.Message,
	) (*agent.RunResult, []llm.Message, error)
}

// Session 持有一段对话的消息历史，并把每个用户轮次交给 TurnExecutor。
// Session 不是并发安全的；同一会话必须由调用方串行使用。
type Session struct {
	executor TurnExecutor
	messages []llm.Message
}

var _ agent.Runner = (*Session)(nil)

// New 创建一个拥有初始消息历史的会话。
// Session 会复制 initialHistory，调用方可以在返回后继续使用原切片。
func New(
	executor TurnExecutor,
	initialHistory []llm.Message,
) (*Session, error) {
	if executor == nil {
		return nil, fmt.Errorf("turn executor is required")
	}

	return &Session{
		executor: executor,
		messages: append([]llm.Message(nil), initialHistory...),
	}, nil
}

// Run 处理一个用户轮次；只有执行成功时才提交新的消息历史。
// 失败或取消不会污染下一次 Run 看到的会话状态。
func (s *Session) Run(
	ctx context.Context,
	input string,
) (*agent.RunResult, error) {
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
