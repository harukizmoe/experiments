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
	argumentAccuracy, argumentComment := toolArgumentAccuracy(item.Expected.RequiredTools, result.ToolCalls)
	precision, recall, unnecessary := toolPrecisionRecall(item.Expected, result.ToolCalls)

	turnsOK := item.Expected.MaxTurns == 0 || result.Turns <= item.Expected.MaxTurns
	toolCountOK := item.Expected.MaxToolCalls == 0 || len(result.ToolCalls) <= item.Expected.MaxToolCalls

	// Keep efficiency metrics separate so task_success does not hide why a run passed.
	taskOK := runErr == nil && answerOK && selectionOK && argumentAccuracy == 1 && turnsOK && toolCountOK

	metrics := []Metric{
		booleanMetric("task_success", taskOK, taskComment(taskOK, runErr, answerOK, selectionOK, argumentAccuracy, turnsOK, toolCountOK)),
		booleanMetric("answer_correctness", answerOK, answerComment(answerOK, item.Expected.AnswerRules, result.Answer)),
		booleanMetric("tool_selection_accuracy", selectionOK, selectionComment),
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

func taskComment(ok bool, runErr error, answerOK, selectionOK bool, argumentAccuracy float64, turnsOK, toolCountOK bool) string {
	if ok {
		return "answer, tool policy, arguments, and configured limits all passed"
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
