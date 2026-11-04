package agent_trace_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(toolList ...Tool) (*Registry, error) {
	r := &Registry{
		tools: make(map[string]Tool, len(toolList)),
	}

	for _, tool := range toolList {
		if tool == nil {
			return nil, fmt.Errorf("tool must not be nil")
		}

		name := tool.Definition().Name
		if name == "" {
			return nil, fmt.Errorf(
				"tool name must not be empty",
			)
		}

		if _, exists := r.tools[name]; exists {
			return nil, fmt.Errorf(
				"duplicate tool: %s",
				name,
			)
		}

		r.tools[name] = tool
	}

	return r, nil
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	names := make([]string, 0, len(r.tools))

	for name := range r.tools {
		names = append(names, name)
	}

	sort.Strings(names)

	definitions := make(
		[]llm.ToolDefinition,
		0,
		len(names),
	)

	for _, name := range names {
		definitions = append(
			definitions,
			r.tools[name].Definition(),
		)
	}

	return definitions
}

func (r *Registry) Execute(
	ctx context.Context,
	call llm.ToolCall,
) (string, error) {

	tool, ok := r.tools[call.Name]

	if !ok {
		return "", fmt.Errorf(
			"unknown tool: %s",
			call.Name,
		)
	}

	if !json.Valid(call.Arguments) {
		return "", fmt.Errorf(
			"invalid JSON arguments for tool %s: %s",
			call.Name,
			string(call.Arguments),
		)
	}

	return tool.Execute(
		ctx,
		call.Arguments,
	)
}
