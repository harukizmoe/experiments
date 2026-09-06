package agent_trace_agent

import "context"

// Runner 是外层组合模块调用一个用户轮次的最小接口。
// Session 和 observability 装饰器实现此接口；Agent 执行内核不持有会话状态。
type Runner interface {
	Run(
		ctx context.Context,
		input string,
	) (*RunResult, error)
}
