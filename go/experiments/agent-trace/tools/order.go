package agent_trace_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type OrderTool struct{}

type order struct {
	OrderID string `json:"order_id"`
	Status  string `json:"status"`
}

var orderData = map[string]order{
	"A100": {
		OrderID: "A100",
		Status:  "shipped",
	},
	"A101": {
		OrderID: "A101",
		Status:  "pending",
	},
	"A102": {
		OrderID: "A102",
		Status:  "refunded",
	},
	"A103": {
		OrderID: "A103",
		Status:  "cancelled",
	},
}

func (OrderTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "lookup_order",

		Description: "Look up the current status of an order by order ID. Use this tool instead of guessing order status.",

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"order_id": map[string]any{
					"type": "string",

					"description": "Order ID such as A100 or A102.",
				},
			},

			"required": []string{
				"order_id",
			},

			"additionalProperties": false,
		},
	}
}

func (OrderTool) Execute(
	_ context.Context,
	arguments json.RawMessage,
) (string, error) {

	var args struct {
		OrderID string `json:"order_id"`
	}

	if err := json.Unmarshal(
		arguments,
		&args,
	); err != nil {

		return "", fmt.Errorf(
			"decode lookup_order arguments: %w",
			err,
		)
	}

	args.OrderID = strings.ToUpper(
		strings.TrimSpace(args.OrderID),
	)

	if args.OrderID == "" {
		return "", fmt.Errorf(
			"order_id is required",
		)
	}

	item, ok := orderData[args.OrderID]

	if !ok {
		return marshalJSON(
			map[string]any{
				"found":    false,
				"order_id": args.OrderID,
			},
		)
	}

	return marshalJSON(
		map[string]any{
			"found":    true,
			"order_id": item.OrderID,
			"status":   item.Status,
		},
	)
}
