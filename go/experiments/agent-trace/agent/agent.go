package agent

import (
	"context"
	"errors"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"
)

// Agent 是无状态的 Runtime 执行适配器。
//
// Agent 不持有对话历史，也不创建 Provider 或工具；历史由 session.Session
// 持有，应用启动时准备好的 runtime.Engine 由程序入口注入。
type Agent struct {
	engine *runtime.Engine
}

// New 创建一个基于已有 Runtime Engine 的 Agent。
//
// New 不发起网络请求，也不会复制或创建会话状态。每个需要独立历史的
// 对话都应把返回的 Agent 注入一个独立的 session.Session。
func New(engine *runtime.Engine) (*Agent, error) {
	if engine == nil {
		return nil, errors.New("runtime engine is required")
	}

	return &Agent{engine: engine}, nil
}

// RunWithHistory 执行一个用户轮次，并把历史读写契约交给 Runtime Engine。
// Agent 不修改调用方拥有的 history；成功时返回下一份历史，失败时返回 nil。
func (a *Agent) RunWithHistory(
	ctx context.Context,
	input string,
	history []llm.Message,
) (*runtime.RunResult, []llm.Message, error) {
	if a == nil || a.engine == nil {
		return nil, nil, errors.New("agent is not initialized")
	}

	return a.engine.RunWithHistory(ctx, input, history)
}
