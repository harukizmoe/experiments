package agent_trace_tools

import (
	"encoding/json"
	"fmt"
)

func marshalJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf(
			"marshal tool result: %w",
			err,
		)
	}

	return string(b), nil
}
