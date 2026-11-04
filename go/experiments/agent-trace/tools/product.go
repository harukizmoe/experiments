package agent_trace_tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
)

type ProductTool struct{}

type product struct {
	ProductID string  `json:"product_id"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Stock     int     `json:"stock"`
	Currency  string  `json:"currency"`
}

var productData = map[string]product{
	"P100": {
		ProductID: "P100",
		Name:      "Basic Keyboard",
		Price:     129,
		Stock:     10,
		Currency:  "CNY",
	},
	"P200": {
		ProductID: "P200",
		Name:      "Wireless Mouse",
		Price:     299,
		Stock:     0,
		Currency:  "CNY",
	},
	"P300": {
		ProductID: "P300",
		Name:      "USB-C Cable",
		Price:     79,
		Stock:     100,
		Currency:  "CNY",
	},
}

func (ProductTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name: "get_product",

		Description: "Get deterministic product information including name, price, currency, and stock by product ID.",

		Parameters: map[string]any{
			"type": "object",

			"properties": map[string]any{
				"product_id": map[string]any{
					"type": "string",

					"description": "Product ID such as P100 or P200.",
				},
			},

			"required": []string{
				"product_id",
			},

			"additionalProperties": false,
		},
	}
}

func (ProductTool) Execute(
	_ context.Context,
	arguments json.RawMessage,
) (string, error) {

	var args struct {
		ProductID string `json:"product_id"`
	}

	if err := json.Unmarshal(
		arguments,
		&args,
	); err != nil {

		return "", fmt.Errorf(
			"decode get_product arguments: %w",
			err,
		)
	}

	args.ProductID = strings.ToUpper(
		strings.TrimSpace(args.ProductID),
	)

	if args.ProductID == "" {
		return "", fmt.Errorf(
			"product_id is required",
		)
	}

	item, ok := productData[args.ProductID]

	if !ok {
		return marshalJSON(
			map[string]any{
				"found":      false,
				"product_id": args.ProductID,
			},
		)
	}

	return marshalJSON(
		map[string]any{
			"found":      true,
			"product_id": item.ProductID,
			"name":       item.Name,
			"price":      item.Price,
			"currency":   item.Currency,
			"stock":      item.Stock,
		},
	)
}
