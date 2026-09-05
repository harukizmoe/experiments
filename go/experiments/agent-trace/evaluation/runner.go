package agent_trace_eval

import (
	"context"
	"fmt"
	"io"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
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

// Runner executes cases sequentially and optionally uploads their scores.
type Runner struct {
	AgentForCase func(Case) agent.Runner
	ScoreSink    ScoreSink
	Progress     io.Writer
}

// CaseResult combines the Agent output, evaluation metrics, and upload errors.
type CaseResult struct {
	CaseID            string           `json:"case_id"`
	Category          string           `json:"category"`
	Difficulty        string           `json:"difficulty,omitempty"`
	Input             string           `json:"input"`
	AgentResult       *agent.RunResult `json:"agent_result,omitempty"`
	AgentError        string           `json:"agent_error,omitempty"`
	Evaluation        Evaluation       `json:"evaluation"`
	ScoreUploadErrors []string         `json:"score_upload_errors,omitempty"`
}

// Run executes each dataset case independently, preserving result order.
func (r Runner) Run(ctx context.Context, dataset Dataset) ([]CaseResult, error) {
	if r.AgentForCase == nil {
		return nil, fmt.Errorf("AgentForCase is required")
	}

	results := make([]CaseResult, 0, len(dataset.Cases))
	for index, item := range dataset.Cases {
		if err := ctx.Err(); err != nil {
			return results, err
		}

		runner := r.AgentForCase(item)
		if runner == nil {
			return results, fmt.Errorf("AgentForCase returned nil for %s", item.ID)
		}

		agentResult, runErr := runner.Run(ctx, item.Input)
		evaluation := Evaluate(item, agentResult, runErr)
		caseResult := CaseResult{
			CaseID:      item.ID,
			Category:    item.Metadata.Category,
			Difficulty:  item.Metadata.Difficulty,
			Input:       item.Input,
			AgentResult: agentResult,
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

func metricValue(metrics []Metric, name string) float64 {
	for _, metric := range metrics {
		if metric.Name == name {
			return metric.Value
		}
	}
	return 0
}
