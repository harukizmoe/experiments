package agent_trace_tools

import (
	"context"
	"encoding/json"
	"math"
	"testing"
)

func TestEvaluateExpression(t *testing.T) {
	tests := []struct {
		expression string
		want       float64
	}{
		{
			"125 * 8",
			1000,
		},
		{
			"(129 * 3) + 20",
			407,
		},
		{
			"10 / 4",
			2.5,
		},
		{
			"-2 * (3 + 4)",
			-14,
		},
		{
			"1 + 2 * 3",
			7,
		},
	}

	for _, tt := range tests {
		t.Run(
			tt.expression,
			func(t *testing.T) {

				got, err := evaluateExpression(
					tt.expression,
				)

				if err != nil {
					t.Fatalf(
						"evaluateExpression() error = %v",
						err,
					)
				}

				if math.Abs(got-tt.want) > 1e-9 {
					t.Fatalf(
						"evaluateExpression() = %v, want %v",
						got,
						tt.want,
					)
				}
			},
		)
	}
}

func TestCalculatorTool(t *testing.T) {
	tool := CalculatorTool{}

	result, err := tool.Execute(
		context.Background(),
		json.RawMessage(
			`{"expression":"129 * 3"}`,
		),
	)

	if err != nil {
		t.Fatalf(
			"Execute() error = %v",
			err,
		)
	}

	if result !=
		`{"expression":"129 * 3","result":387}` {

		t.Fatalf(
			"Execute() = %s",
			result,
		)
	}
}
