package agent_trace_runtime

import (
	"encoding/json"
	"time"

	llm "harukizmoe/experiments/go/experiments/agent-trace/llm"
)

// ToolCallRecord 描述 Runtime 在一次用户轮次中执行的单个工具调用及其结果。
type ToolCallRecord struct {
	Turn      int             `json:"turn"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Latency   time.Duration   `json:"latency"`
}

// RunResult 是一次用户轮次的同步执行结果，包含回答、工具调用、用量和观测标识。
type RunResult struct {
	Input             string           `json:"input"`
	Answer            string           `json:"answer"`
	Turns             int              `json:"turns"`
	ToolCalls         []ToolCallRecord `json:"tool_calls"`
	Usage             llm.Usage        `json:"usage"`
	Latency           time.Duration    `json:"latency"`
	FinalReason       string           `json:"final_reason"`
	TraceID           string           `json:"trace_id,omitempty"`
	RootObservationID string           `json:"root_observation_id,omitempty"`
}
