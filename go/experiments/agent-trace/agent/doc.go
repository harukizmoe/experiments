// Package agent 提供无状态的 Agent 执行适配器。
//
// Agent 只接收已经组装好的 runtime.Engine，不持有会话历史，不读取进程
// 环境，也不创建 Provider 或工具。需要多轮状态时，使用独立的 session
// 包持有历史并把 Agent 作为 TurnExecutor 注入。
package agent
