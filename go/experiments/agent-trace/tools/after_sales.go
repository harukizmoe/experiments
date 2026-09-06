package agent_trace_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

// OrderDetailsTool 返回退货判断所需的订单事实，不执行任何订单状态变更。
type OrderDetailsTool struct{}

// ReturnPolicyTool 返回固定的退货窗口、允许状态和退款规则。
type ReturnPolicyTool struct{}

func (OrderDetailsTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "get_order_details",
		Description: "Get the order facts needed for a read-only return eligibility check: status, product ID, quantity, and fixed days since purchase. Use this tool instead of guessing.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{
					"type":        "string",
					"description": "Order ID such as A100 or A104.",
				},
			},
			"required":             []string{"order_id"},
			"additionalProperties": false,
		},
	}
}

func (OrderDetailsTool) Execute(
	_ context.Context,
	arguments json.RawMessage,
) (string, error) {
	var args struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(arguments, &args); err != nil {
		return "", fmt.Errorf("decode get_order_details arguments: %w", err)
	}

	args.OrderID = strings.ToUpper(strings.TrimSpace(args.OrderID))
	if args.OrderID == "" {
		return "", fmt.Errorf("order_id is required")
	}

	item, ok := orderData[args.OrderID]
	if !ok {
		return marshalJSON(map[string]any{
			"found":    false,
			"order_id": args.OrderID,
		})
	}

	return marshalJSON(map[string]any{
		"found":               true,
		"order_id":            item.OrderID,
		"status":              item.Status,
		"product_id":          item.ProductID,
		"quantity":            item.Quantity,
		"days_since_purchase": item.DaysSincePurchase,
	})
}

func (ReturnPolicyTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "get_return_policy",
		Description: "Get the deterministic read-only return policy. It has no arguments and returns the return window, eligible order statuses, and refund policy.",
		Parameters: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (ReturnPolicyTool) Execute(
	_ context.Context,
	arguments json.RawMessage,
) (string, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &args); err != nil {
		return "", fmt.Errorf("decode get_return_policy arguments: %w", err)
	}

	return marshalJSON(map[string]any{
		"return_window_days": 30,
		"eligible_statuses":  []string{"shipped", "delivered"},
		"refund_policy":      "full_original_price",
	})
}
