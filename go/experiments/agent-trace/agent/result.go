package agent_trace_agent

import (
	"encoding/json"
	"time"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type ToolCallRecord struct {
	Turn      int             `json:"turn"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    string          `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Latency   time.Duration   `json:"latency"`
}

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
