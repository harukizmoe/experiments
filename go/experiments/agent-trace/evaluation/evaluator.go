package agent_trace_eval

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
)

const (
	// DataTypeBoolean is the Langfuse boolean score type.
	DataTypeBoolean = "BOOLEAN"
	// DataTypeNumeric is the Langfuse numeric score type.
	DataTypeNumeric = "NUMERIC"
)

// Metric is one deterministic score calculated for an evaluation case.
type Metric struct {
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	DataType string  `json:"data_type"`
	Comment  string  `json:"comment,omitempty"`
}

// Evaluation contains case-level pass/fail state and its diagnostic metrics.
type Evaluation struct {
	CaseID  string   `json:"case_id"`
	Passed  bool     `json:"passed"`
	Metrics []Metric `json:"metrics"`
}

// Evaluate compares an Agent result with one case's observable expectations.
func Evaluate(item Case, result *agent.RunResult, runErr error) Evaluation {
	if result == nil {
		result = &agent.RunResult{}
	}

	answerOK := answerMatches(item.Expected.AnswerRules, result.Answer)
	selectionOK, selectionComment := toolSelection(item.Expected, result.ToolCalls)
	orderOK, orderComment := toolOrder(item.Expected.ToolOrder, result.ToolCalls)
	businessOK, businessComment := businessOutcomeCorrect(item.Expected, result.ToolCalls)
	argumentAccuracy, argumentComment := toolArgumentAccuracy(item.Expected.RequiredTools, result.ToolCalls)
	precision, recall, unnecessary := toolPrecisionRecall(item.Expected, result.ToolCalls)

	turnsOK := item.Expected.MaxTurns == 0 || result.Turns <= item.Expected.MaxTurns
	toolCountOK := item.Expected.MaxToolCalls == 0 || len(result.ToolCalls) <= item.Expected.MaxToolCalls

	// 业务结果和工具顺序属于任务正确性；效率指标仍单独保留，便于定位退化原因。
	taskOK := runErr == nil && answerOK && selectionOK && orderOK && businessOK && argumentAccuracy == 1 && turnsOK && toolCountOK

	metrics := []Metric{
		booleanMetric("task_success", taskOK, taskComment(taskOK, runErr, answerOK, selectionOK, orderOK, businessOK, argumentAccuracy, turnsOK, toolCountOK)),
		booleanMetric("answer_correctness", answerOK, answerComment(answerOK, item.Expected.AnswerRules, result.Answer)),
		booleanMetric("tool_selection_accuracy", selectionOK, selectionComment),
		booleanMetric("tool_order_accuracy", orderOK, orderComment),
		booleanMetric("business_outcome_correctness", businessOK, businessComment),
		{
			Name:     "tool_argument_accuracy",
			Value:    argumentAccuracy,
			DataType: DataTypeNumeric,
			Comment:  argumentComment,
		},
		{
			Name:     "tool_precision",
			Value:    precision,
			DataType: DataTypeNumeric,
			Comment:  fmt.Sprintf("relevant tool-call precision %.3f", precision),
		},
		{
			Name:     "tool_recall",
			Value:    recall,
			DataType: DataTypeNumeric,
			Comment:  fmt.Sprintf("required tool-call recall %.3f", recall),
		},
		{
			Name:     "unnecessary_tool_calls",
			Value:    float64(unnecessary),
			DataType: DataTypeNumeric,
			Comment:  fmt.Sprintf("%d tool calls exceeded the allowed tool quota", unnecessary),
		},
		{
			Name:     "turn_count",
			Value:    float64(result.Turns),
			DataType: DataTypeNumeric,
			Comment:  limitComment("turns", result.Turns, item.Expected.MaxTurns),
		},
		{
			Name:     "tool_call_count",
			Value:    float64(len(result.ToolCalls)),
			DataType: DataTypeNumeric,
			Comment:  limitComment("tool calls", len(result.ToolCalls), item.Expected.MaxToolCalls),
		},
	}

	return Evaluation{
		CaseID:  item.ID,
		Passed:  taskOK,
		Metrics: metrics,
	}
}

func booleanMetric(name string, ok bool, comment string) Metric {
	value := 0.0
	if ok {
		value = 1
	}
	return Metric{Name: name, Value: value, DataType: DataTypeBoolean, Comment: comment}
}

func answerMatches(rules []AnswerRule, answer string) bool {
	if len(rules) == 0 {
		return true
	}
	actual := strings.ToLower(answer)
	for _, rule := range rules {
		matched := false
		for _, alternative := range rule.AnyOf {
			needle := strings.ToLower(strings.TrimSpace(alternative))
			if needle != "" && strings.Contains(actual, needle) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func toolSelection(expected Expected, actual []agent.ToolCallRecord) (bool, string) {
	required := toolCountsFromExpectations(expected.RequiredTools)
	actualCounts := toolCounts(actual)

	for name, count := range required {
		if actualCounts[name] < count {
			return false, fmt.Sprintf("required tool %s expected %d call(s), got %d", name, count, actualCounts[name])
		}
	}

	forbidden := stringSet(expected.ForbiddenTools)
	for name := range actualCounts {
		if forbidden[name] {
			return false, fmt.Sprintf("forbidden tool %s was called", name)
		}
	}

	allowed := map[string]bool{}
	for name := range required {
		allowed[name] = true
	}
	for _, name := range expected.OptionalTools {
		allowed[name] = true
	}
	for name := range actualCounts {
		if !allowed[name] {
			return false, fmt.Sprintf("unapproved tool %s was called", name)
		}
	}

	if expected.MaxToolCalls > 0 && len(actual) > expected.MaxToolCalls {
		return false, fmt.Sprintf("tool call count %d exceeds max %d", len(actual), expected.MaxToolCalls)
	}
	return true, "required tools present and no forbidden/unapproved tool was used"
}

// toolOrder 检查有依赖关系的工具是否按规定顺序出现。
func toolOrder(expected []string, actual []agent.ToolCallRecord) (bool, string) {
	if len(expected) == 0 {
		return true, "no tool order configured"
	}

	position := 0
	for _, call := range actual {
		if call.Name != expected[position] {
			continue
		}
		position++
		if position == len(expected) {
			return true, "configured tool order was observed"
		}
	}

	actualNames := make([]string, 0, len(actual))
	for _, call := range actual {
		actualNames = append(actualNames, call.Name)
	}
	return false, fmt.Sprintf(
		"tool order mismatch: expected %s, got %s",
		strings.Join(expected, " -> "),
		strings.Join(actualNames, " -> "),
	)
}

type observedOrderDetails struct {
	Found             bool   `json:"found"`
	OrderID           string `json:"order_id"`
	Status            string `json:"status"`
	ProductID         string `json:"product_id"`
	Quantity          int    `json:"quantity"`
	DaysSincePurchase int    `json:"days_since_purchase"`
}

type observedProduct struct {
	Found     bool    `json:"found"`
	ProductID string  `json:"product_id"`
	Price     float64 `json:"price"`
}

type observedReturnPolicy struct {
	ReturnWindowDays int      `json:"return_window_days"`
	EligibleStatuses []string `json:"eligible_statuses"`
	RefundPolicy     string   `json:"refund_policy"`
}

type observedCalculation struct {
	Result float64 `json:"result"`
}

// businessOutcomeCorrect 根据与期望参数绑定的只读工具结果推导业务判断。
func businessOutcomeCorrect(expected Expected, calls []agent.ToolCallRecord) (bool, string) {
	outcome := expected.ExpectedBusinessOutcome
	if outcome == nil {
		return true, "no expected business outcome configured"
	}

	orderArguments, ok := expectedToolArguments(expected.RequiredTools, "get_order_details")
	if !ok {
		return false, "business outcome requires get_order_details arguments"
	}
	expectedOrderID, ok := expectedStringArgument(orderArguments, "order_id")
	if !ok {
		return false, "get_order_details expectation must include order_id"
	}

	var order observedOrderDetails
	if err := decodeToolResult("get_order_details", orderArguments, calls, &order); err != nil {
		return false, err.Error()
	}
	if !order.Found {
		return false, "get_order_details reported that the order was not found"
	}
	if !strings.EqualFold(expectedOrderID, strings.TrimSpace(order.OrderID)) {
		return false, fmt.Sprintf("get_order_details returned order_id %q, want %q", order.OrderID, expectedOrderID)
	}
	if strings.TrimSpace(order.ProductID) == "" {
		return false, "get_order_details returned an empty product_id"
	}

	productArguments, ok := expectedToolArguments(expected.RequiredTools, "get_product")
	if !ok {
		return false, "business outcome requires get_product arguments"
	}
	expectedProductID, ok := expectedStringArgument(productArguments, "product_id")
	if !ok {
		return false, "get_product expectation must include product_id"
	}
	if !strings.EqualFold(expectedProductID, strings.TrimSpace(order.ProductID)) {
		return false, fmt.Sprintf("expected product_id %q does not match order product_id %q", expectedProductID, order.ProductID)
	}

	policyArguments, ok := expectedToolArguments(expected.RequiredTools, "get_return_policy")
	if !ok {
		return false, "business outcome requires get_return_policy"
	}
	var policy observedReturnPolicy
	if err := decodeToolResult("get_return_policy", policyArguments, calls, &policy); err != nil {
		return false, err.Error()
	}
	if policy.ReturnWindowDays < 0 || len(policy.EligibleStatuses) == 0 {
		return false, "get_return_policy returned an invalid policy"
	}

	statusAllowed := statusInList(policy.EligibleStatuses, order.Status)
	withinWindow := order.DaysSincePurchase >= 0 && order.DaysSincePurchase <= policy.ReturnWindowDays
	actualEligible := statusAllowed && withinWindow
	actualReasons := make([]string, 0, 2)
	if statusAllowed {
		actualReasons = append(actualReasons, "status_allowed")
	} else {
		actualReasons = append(actualReasons, "status_not_allowed")
	}
	if withinWindow {
		actualReasons = append(actualReasons, "within_return_window")
	} else {
		actualReasons = append(actualReasons, "outside_return_window")
	}

	if !actualEligible {
		for _, call := range calls {
			if call.Name == "calculator" {
				return false, "calculator must not be called for an ineligible order"
			}
		}
	}

	var product observedProduct
	if err := decodeToolResult("get_product", productArguments, calls, &product); err != nil {
		return false, err.Error()
	}
	if !product.Found || product.Price < 0 {
		return false, "get_product returned invalid product facts"
	}
	if !strings.EqualFold(strings.TrimSpace(product.ProductID), strings.TrimSpace(order.ProductID)) {
		return false, fmt.Sprintf("get_product returned product_id %q, want order product_id %q", product.ProductID, order.ProductID)
	}

	actualRefund := 0.0
	if actualEligible {
		if order.Quantity <= 0 {
			return false, "get_order_details returned a non-positive quantity"
		}
		if policy.RefundPolicy != "full_original_price" {
			return false, fmt.Sprintf("unsupported refund policy %q", policy.RefundPolicy)
		}

		calculatorArguments, ok := expectedToolArguments(expected.RequiredTools, "calculator")
		if !ok {
			return false, "eligible business outcome requires calculator arguments"
		}
		if _, ok := expectedStringArgument(calculatorArguments, "expression"); !ok {
			return false, "calculator expectation must include expression"
		}
		var calculation observedCalculation
		if err := decodeToolResult("calculator", calculatorArguments, calls, &calculation); err != nil {
			return false, err.Error()
		}
		actualRefund = calculation.Result
		calculatedRefund := product.Price * float64(order.Quantity)
		if !closeEnough(actualRefund, calculatedRefund) {
			return false, fmt.Sprintf("calculator result %.2f does not equal original-price refund %.2f", actualRefund, calculatedRefund)
		}
	}

	if outcome.Eligible != actualEligible {
		return false, fmt.Sprintf("eligible=%t, want %t", actualEligible, outcome.Eligible)
	}
	if !closeEnough(outcome.RefundAmount, actualRefund) {
		return false, fmt.Sprintf("refund_amount=%.2f, want %.2f", actualRefund, outcome.RefundAmount)
	}
	if len(outcome.ReasonCodes) > 0 && !sameStrings(outcome.ReasonCodes, actualReasons) {
		return false, fmt.Sprintf("reason_codes=%v, want %v", actualReasons, outcome.ReasonCodes)
	}
	return true, fmt.Sprintf("eligible=%t refund_amount=%.2f reason_codes=%v", actualEligible, actualRefund, actualReasons)
}

// decodeToolResult 解码与期望参数匹配的唯一工具结果；重复匹配直接拒绝。
func decodeToolResult(name string, expectedArguments map[string]any, calls []agent.ToolCallRecord, target any) error {
	matches := 0
	for _, call := range calls {
		if call.Name != name {
			continue
		}
		if len(expectedArguments) > 0 && !argumentsMatch(name, expectedArguments, call.Arguments) {
			continue
		}

		matches++
		if matches > 1 {
			return fmt.Errorf("%s has multiple results matching expected arguments", name)
		}
		if call.Error != "" {
			return fmt.Errorf("%s failed: %s", name, call.Error)
		}
		if strings.TrimSpace(call.Result) == "" {
			return fmt.Errorf("%s returned an empty result", name)
		}
		if err := json.Unmarshal([]byte(call.Result), target); err != nil {
			return fmt.Errorf("decode %s result: %w", name, err)
		}
	}
	if matches == 0 {
		return fmt.Errorf("%s result matching expected arguments was not found", name)
	}
	return nil
}

func expectedToolArguments(expected []ToolExpectation, name string) (map[string]any, bool) {
	for _, item := range expected {
		if item.Name == name {
			return item.Arguments, true
		}
	}
	return nil, false
}

func expectedStringArgument(arguments map[string]any, name string) (string, bool) {
	value, ok := arguments[name].(string)
	if !ok {
		return "", false
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}

func statusInList(statuses []string, status string) bool {
	for _, candidate := range statuses {
		if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(status)) {
			return true
		}
	}
	return false
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := stringSet(left)
	rightSet := stringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if !rightSet[value] {
			return false
		}
	}
	return true
}

func closeEnough(left, right float64) bool {
	return math.Abs(left-right) < 1e-9
}

func toolArgumentAccuracy(expected []ToolExpectation, actual []agent.ToolCallRecord) (float64, string) {
	var withArguments []ToolExpectation
	for _, item := range expected {
		if len(item.Arguments) > 0 {
			withArguments = append(withArguments, item)
		}
	}
	if len(withArguments) == 0 {
		return 1, "no required tool arguments to compare"
	}

	used := make([]bool, len(actual))
	matched := 0
	for _, wanted := range withArguments {
		for i, call := range actual {
			if used[i] || call.Name != wanted.Name {
				continue
			}
			if argumentsMatch(wanted.Name, wanted.Arguments, call.Arguments) {
				used[i] = true
				matched++
				break
			}
		}
	}
	value := float64(matched) / float64(len(withArguments))
	return value, fmt.Sprintf("matched %d/%d required tool argument sets", matched, len(withArguments))
}

func toolPrecisionRecall(expected Expected, actual []agent.ToolCallRecord) (precision, recall float64, unnecessary int) {
	requiredQuota := toolCountsFromExpectations(expected.RequiredTools)
	allowedQuota := map[string]int{}
	for name, count := range requiredQuota {
		allowedQuota[name] = count
	}
	for _, name := range expected.OptionalTools {
		allowedQuota[name]++
	}

	relevant := 0
	remaining := cloneCounts(allowedQuota)
	for _, call := range actual {
		if remaining[call.Name] > 0 {
			relevant++
			remaining[call.Name]--
		} else {
			unnecessary++
		}
	}

	if len(actual) == 0 {
		if len(expected.RequiredTools) == 0 {
			precision = 1
		} else {
			precision = 0
		}
	} else {
		precision = float64(relevant) / float64(len(actual))
	}

	requiredTotal := 0
	requiredMatched := 0
	actualCounts := toolCounts(actual)
	for name, count := range requiredQuota {
		requiredTotal += count
		requiredMatched += min(count, actualCounts[name])
	}
	if requiredTotal == 0 {
		recall = 1
	} else {
		recall = float64(requiredMatched) / float64(requiredTotal)
	}
	return precision, recall, unnecessary
}

func argumentsMatch(toolName string, expected map[string]any, raw json.RawMessage) bool {
	var actual any
	if err := json.Unmarshal(raw, &actual); err != nil {
		return false
	}
	actualMap, ok := actual.(map[string]any)
	if !ok {
		return false
	}
	return valueSubset(toolName, "", expected, actualMap)
}

// valueSubset intentionally accepts extra actual fields so valid trajectories can
// carry provider-specific arguments while required fields remain verifiable.
func valueSubset(toolName, key string, expected, actual any) bool {
	switch wanted := expected.(type) {
	case map[string]any:
		got, ok := actual.(map[string]any)
		if !ok {
			return false
		}
		for childKey, childWanted := range wanted {
			childGot, exists := got[childKey]
			if !exists || !valueSubset(toolName, childKey, childWanted, childGot) {
				return false
			}
		}
		return true
	case []any:
		got, ok := actual.([]any)
		if !ok || len(got) != len(wanted) {
			return false
		}
		for i := range wanted {
			if !valueSubset(toolName, key, wanted[i], got[i]) {
				return false
			}
		}
		return true
	case string:
		got, ok := actual.(string)
		if !ok {
			return false
		}
		if toolName == "calculator" && key == "expression" {
			return removeWhitespace(strings.ToLower(wanted)) == removeWhitespace(strings.ToLower(got))
		}
		return strings.EqualFold(strings.TrimSpace(wanted), strings.TrimSpace(got))
	case float64:
		got, ok := actual.(float64)
		return ok && math.Abs(wanted-got) < 1e-9
	case bool:
		got, ok := actual.(bool)
		return ok && wanted == got
	case nil:
		return actual == nil
	default:
		return fmt.Sprint(wanted) == fmt.Sprint(actual)
	}
}

func removeWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func toolCounts(calls []agent.ToolCallRecord) map[string]int {
	counts := map[string]int{}
	for _, call := range calls {
		counts[call.Name]++
	}
	return counts
}

func toolCountsFromExpectations(items []ToolExpectation) map[string]int {
	counts := map[string]int{}
	for _, item := range items {
		counts[item.Name]++
	}
	return counts
}

func stringSet(items []string) map[string]bool {
	set := map[string]bool{}
	for _, item := range items {
		set[item] = true
	}
	return set
}

func cloneCounts(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func answerComment(ok bool, rules []AnswerRule, answer string) string {
	if len(rules) == 0 {
		return "no answer rule configured"
	}
	if ok {
		return "answer satisfied all configured substring rules"
	}
	return fmt.Sprintf("answer did not satisfy all rules; answer=%q", truncate(answer, 180))
}

func taskComment(ok bool, runErr error, answerOK, selectionOK, orderOK, businessOK bool, argumentAccuracy float64, turnsOK, toolCountOK bool) string {
	if ok {
		return "answer, tool policy, order, business outcome, arguments, and configured limits all passed"
	}
	parts := []string{}
	if runErr != nil {
		parts = append(parts, "agent_error="+runErr.Error())
	}
	if !answerOK {
		parts = append(parts, "answer_failed")
	}
	if !selectionOK {
		parts = append(parts, "tool_selection_failed")
	}
	if !orderOK {
		parts = append(parts, "tool_order_failed")
	}
	if !businessOK {
		parts = append(parts, "business_outcome_failed")
	}
	if argumentAccuracy < 1 {
		parts = append(parts, "tool_arguments_failed")
	}
	if !turnsOK {
		parts = append(parts, "max_turns_exceeded")
	}
	if !toolCountOK {
		parts = append(parts, "max_tool_calls_exceeded")
	}
	return strings.Join(parts, ", ")
}

func limitComment(name string, actual, limit int) string {
	if limit <= 0 {
		return fmt.Sprintf("%s=%d; no case-level limit", name, actual)
	}
	return fmt.Sprintf("%s=%d; max=%d", name, actual, limit)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
