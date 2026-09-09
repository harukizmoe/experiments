package agent_trace_eval

import (
	"context"
	"fmt"
	"io"

	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"
)

// ScoreSink receives one metric for a completed case.
type ScoreSink interface {
	WriteScore(
		ctx context.Context,
		traceID string,
		name string,
		value float64,
		dataType string,
		comment string,
	) error
}

// Runner 按顺序执行 Dataset case，并可选地上传评估分数。
type Runner struct {
	RunnerForCase func(Case) (runtime.Runner, error)
	ScoreSink     ScoreSink
	Progress      io.Writer
}

// ConversationTurnResult 记录一条用户输入及该轮 Runtime 结果。
// CaseResult 的聚合结果用于评分；逐轮证据保留澄清和工具发生轮次，写入本地报告。
type ConversationTurnResult struct {
	Turn        int                `json:"turn"`
	Input       string             `json:"input"`
	AgentResult *runtime.RunResult `json:"agent_result,omitempty"`
	AgentError  string             `json:"agent_error,omitempty"`
}

// CaseResult 记录聚合 Runtime 输出、逐轮证据、评估指标和上传错误。
type CaseResult struct {
	CaseID            string                   `json:"case_id"`
	Category          string                   `json:"category"`
	Difficulty        string                   `json:"difficulty,omitempty"`
	Input             string                   `json:"input"`
	FollowUps         []string                 `json:"follow_ups,omitempty"`
	AgentResult       *runtime.RunResult       `json:"agent_result,omitempty"`
	TurnResults       []ConversationTurnResult `json:"turn_results,omitempty"`
	AgentError        string                   `json:"agent_error,omitempty"`
	Evaluation        Evaluation               `json:"evaluation"`
	ScoreUploadErrors []string                 `json:"score_upload_errors,omitempty"`
}

// Run 按顺序执行每个 Dataset case，并保持结果顺序。
func (r Runner) Run(ctx context.Context, dataset Dataset) ([]CaseResult, error) {
	if r.RunnerForCase == nil {
		return nil, fmt.Errorf("RunnerForCase is required")
	}

	results := make([]CaseResult, 0, len(dataset.Cases))
	for index, item := range dataset.Cases {
		if err := ctx.Err(); err != nil {
			return results, err
		}

		runner, err := r.RunnerForCase(item)
		if err != nil {
			return results, fmt.Errorf(
				"create runner for %s: %w",
				item.ID,
				err,
			)
		}
		if runner == nil {
			return results, fmt.Errorf("RunnerForCase returned nil for %s", item.ID)
		}
		turnResults, runErr := runConversation(ctx, runner, item)
		rawResults := make([]*runtime.RunResult, 0, len(turnResults))
		for _, turnResult := range turnResults {
			rawResults = append(rawResults, turnResult.AgentResult)
		}
		agentResult := aggregateRunResults(rawResults)
		evaluation := EvaluateConversation(item, rawResults, runErr)
		caseResult := CaseResult{
			CaseID:      item.ID,
			Category:    item.Metadata.Category,
			Difficulty:  item.Metadata.Difficulty,
			Input:       item.Input,
			FollowUps:   item.FollowUps,
			AgentResult: agentResult,
			TurnResults: turnResults,
			Evaluation:  evaluation,
		}
		if runErr != nil {
			caseResult.AgentError = runErr.Error()
		}

		if r.ScoreSink != nil && agentResult != nil && agentResult.TraceID != "" {
			for _, metric := range evaluation.Metrics {
				if err := r.ScoreSink.WriteScore(
					ctx,
					agentResult.TraceID,
					metric.Name,
					metric.Value,
					metric.DataType,
					metric.Comment,
				); err != nil {
					caseResult.ScoreUploadErrors = append(
						caseResult.ScoreUploadErrors,
						fmt.Sprintf("%s: %v", metric.Name, err),
					)
				}
			}
		}

		results = append(results, caseResult)
		if r.Progress != nil {
			task := metricValue(evaluation.Metrics, "task_success")
			selection := metricValue(evaluation.Metrics, "tool_selection_accuracy")
			args := metricValue(evaluation.Metrics, "tool_argument_accuracy")
			traceID := "-"
			if agentResult != nil && agentResult.TraceID != "" {
				traceID = agentResult.TraceID
			}
			_, _ = fmt.Fprintf(
				r.Progress,
				"[%02d/%02d] %-22s category=%-20s task=%.0f tool=%.0f args=%.2f trace=%s\n",
				index+1,
				len(dataset.Cases),
				item.ID,
				item.Metadata.Category,
				task,
				selection,
				args,
				traceID,
			)
		}
	}
	return results, nil
}

func runConversation(
	ctx context.Context,
	runner runtime.Runner,
	item Case,
) ([]ConversationTurnResult, error) {
	inputs := item.userInputs()
	turnResults := make([]ConversationTurnResult, 0, len(inputs))
	for index, input := range inputs {
		if err := ctx.Err(); err != nil {
			return turnResults, err
		}

		result, err := runner.Run(ctx, input)
		turnResult := ConversationTurnResult{
			Turn:        index + 1,
			Input:       input,
			AgentResult: result,
		}
		if err != nil {
			turnResult.AgentError = err.Error()
		}
		turnResults = append(turnResults, turnResult)
		if err != nil {
			return turnResults, err
		}
	}
	return turnResults, nil
}

func aggregateRunResults(results []*runtime.RunResult) *runtime.RunResult {
	var aggregate *runtime.RunResult
	for _, result := range results {
		if result == nil {
			continue
		}
		if aggregate == nil {
			aggregate = &runtime.RunResult{
				Input:             result.Input,
				Answer:            result.Answer,
				Turns:             result.Turns,
				ToolCalls:         append([]runtime.ToolCallRecord(nil), result.ToolCalls...),
				Usage:             result.Usage,
				Latency:           result.Latency,
				FinalReason:       result.FinalReason,
				TraceID:           result.TraceID,
				RootObservationID: result.RootObservationID,
			}
			continue
		}

		aggregate.Input = result.Input
		aggregate.Answer = result.Answer
		aggregate.Turns += result.Turns
		aggregate.ToolCalls = append(aggregate.ToolCalls, result.ToolCalls...)
		aggregate.Usage.Add(result.Usage)
		aggregate.Latency += result.Latency
		aggregate.FinalReason = result.FinalReason
		if result.TraceID != "" {
			aggregate.TraceID = result.TraceID
		}
		if result.RootObservationID != "" {
			aggregate.RootObservationID = result.RootObservationID
		}
	}
	return aggregate
}

func metricValue(metrics []Metric, name string) float64 {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value
		}
	}
	return 0
}
