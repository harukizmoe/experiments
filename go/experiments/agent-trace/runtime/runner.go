package agent_trace_runtime

import "context"

// Runner 是外层组合模块调用一个用户轮次的最小接口。
// Session 和 observability 装饰器实现此接口；Engine 不持有会话状态。
type Runner interface {
	Run(
		ctx context.Context,
		input string,
	) (*RunResult, error)
}
