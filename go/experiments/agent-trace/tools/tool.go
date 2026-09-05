package agent_trace_tools

import (
	"context"
	"encoding/json"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type Tool interface {
	Definition() llm.ToolDefinition

	Execute(
		ctx context.Context,
		arguments json.RawMessage,
	) (string, error)
}
