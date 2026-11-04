package agent_trace_eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Report is the durable local output of one evaluation run.
type Report struct {
	RunName     string       `json:"run_name"`
	DatasetPath string       `json:"dataset_path"`
	Model       string       `json:"model"`
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	Results     []CaseResult `json:"results"`
	Summary     Summary      `json:"summary"`
}

// Summary contains aggregate metrics and category-level task success.
type Summary struct {
	Cases             int                        `json:"cases"`
	Passed            int                        `json:"passed"`
	Failed            int                        `json:"failed"`
	AgentErrors       int                        `json:"agent_errors"`
	ScoreUploadErrors int                        `json:"score_upload_errors"`
	MetricAverages    map[string]float64         `json:"metric_averages"`
	Categories        map[string]CategorySummary `json:"categories"`
}

// CategorySummary reports the number and task success average for one category.
type CategorySummary struct {
	Cases          int     `json:"cases"`
	TaskSuccessAvg float64 `json:"task_success_avg"`
}

// BuildReport creates a report and computes its summary once.
func BuildReport(runName, datasetPath, model string, startedAt time.Time, results []CaseResult) Report {
	return Report{
		RunName:     runName,
		DatasetPath: datasetPath,
		Model:       model,
		StartedAt:   startedAt,
		CompletedAt: time.Now(),
		Results:     results,
		Summary:     Summarize(results),
	}
}

// Summarize averages each emitted metric and groups task success by category.
func Summarize(results []CaseResult) Summary {
	summary := Summary{
		Cases:          len(results),
		MetricAverages: map[string]float64{},
		Categories:     map[string]CategorySummary{},
	}
	metricSums := map[string]float64{}
	metricCounts := map[string]int{}
	categorySuccess := map[string]float64{}

	for _, result := range results {
		if result.Evaluation.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		if result.AgentError != "" {
			summary.AgentErrors++
		}
		summary.ScoreUploadErrors += len(result.ScoreUploadErrors)

		category := result.Category
		if category == "" {
			category = "uncategorized"
		}
		cat := summary.Categories[category]
		cat.Cases++

		for _, metric := range result.Evaluation.Metrics {
			metricSums[metric.Name] += metric.Value
			metricCounts[metric.Name]++
			if metric.Name == "task_success" {
				categorySuccess[category] += metric.Value
			}
		}
		summary.Categories[category] = cat
	}

	for name, sum := range metricSums {
		summary.MetricAverages[name] = sum / float64(metricCounts[name])
	}
	for category, cat := range summary.Categories {
		if cat.Cases > 0 {
			cat.TaskSuccessAvg = categorySuccess[category] / float64(cat.Cases)
		}
		summary.Categories[category] = cat
	}
	return summary
}

// SaveReport writes a readable JSON report, creating its parent directory.
func SaveReport(path string, report Report) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal eval report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write eval report: %w", err)
	}
	return nil
}

// SortedMetricNames returns stable metric ordering for terminal output.
func SortedMetricNames(summary Summary) []string {
	names := make([]string, 0, len(summary.MetricAverages))
	for name := range summary.MetricAverages {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SortedCategoryNames returns stable category ordering for terminal output.
func SortedCategoryNames(summary Summary) []string {
	names := make([]string, 0, len(summary.Categories))
	for name := range summary.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
