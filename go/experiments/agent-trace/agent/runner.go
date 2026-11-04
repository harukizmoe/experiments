package agent_trace_agent

import "context"

// Runner is the minimal interface exposed by an agent implementation.
// Observability and evaluation layers can decorate this interface without
// coupling the core Agent to those concerns.
type Runner interface {
	Run(
		ctx context.Context,
		input string,
	) (*RunResult, error)
}
