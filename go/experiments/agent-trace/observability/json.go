package agent_trace_observability

import "encoding/json"

func jsonString(value any) string {
	data, err := json.Marshal(value)

	if err != nil {
		fallback, _ := json.Marshal(
			map[string]string{
				"serialization_error": err.Error(),
			},
		)

		return string(fallback)
	}

	return string(data)
}

func jsonOrString(
	value string,
) string {

	if json.Valid(
		[]byte(value),
	) {
		return value
	}

	return jsonString(value)
}
