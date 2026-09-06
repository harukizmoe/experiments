package agent_trace_tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOrderDetailsToolReturnsDeterministicFacts(t *testing.T) {
	result, err := (OrderDetailsTool{}).Execute(
		context.Background(),
		json.RawMessage(`{"order_id":"a100"}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got struct {
		Found             bool   `json:"found"`
		OrderID           string `json:"order_id"`
		Status            string `json:"status"`
		ProductID         string `json:"product_id"`
		Quantity          int    `json:"quantity"`
		DaysSincePurchase int    `json:"days_since_purchase"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	want := struct {
		Found             bool
		OrderID           string
		Status            string
		ProductID         string
		Quantity          int
		DaysSincePurchase int
	}{true, "A100", "shipped", "P100", 1, 5}
	if got.Found != want.Found ||
		got.OrderID != want.OrderID ||
		got.Status != want.Status ||
		got.ProductID != want.ProductID ||
		got.Quantity != want.Quantity ||
		got.DaysSincePurchase != want.DaysSincePurchase {
		t.Fatalf("facts = %+v, want %+v", got, want)
	}
}

func TestOrderDetailsToolRejectsMissingOrderID(t *testing.T) {
	_, err := (OrderDetailsTool{}).Execute(
		context.Background(),
		json.RawMessage(`{"order_id":" "}`),
	)
	if err == nil {
		t.Fatal("Execute() error = nil, want missing order_id error")
	}
}

func TestReturnPolicyToolReturnsDeterministicPolicy(t *testing.T) {
	result, err := (ReturnPolicyTool{}).Execute(
		context.Background(),
		json.RawMessage(`{}`),
	)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var got struct {
		ReturnWindowDays int      `json:"return_window_days"`
		EligibleStatuses []string `json:"eligible_statuses"`
		RefundPolicy     string   `json:"refund_policy"`
	}
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("decode result: %v", err)
	}

	if got.ReturnWindowDays != 30 {
		t.Fatalf("return_window_days = %d, want 30", got.ReturnWindowDays)
	}
	if len(got.EligibleStatuses) != 2 ||
		got.EligibleStatuses[0] != "shipped" ||
		got.EligibleStatuses[1] != "delivered" {
		t.Fatalf("eligible_statuses = %#v, want shipped and delivered", got.EligibleStatuses)
	}
	if got.RefundPolicy != "full_original_price" {
		t.Fatalf("refund_policy = %q, want full_original_price", got.RefundPolicy)
	}
}
