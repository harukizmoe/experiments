package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
	config "harukizmoe/go-exp/experiments/agent-trace/config"
	evalpkg "harukizmoe/go-exp/experiments/agent-trace/evaluation"
	langfuse "harukizmoe/go-exp/experiments/agent-trace/langfuse"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	observability "harukizmoe/go-exp/experiments/agent-trace/observability"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		fatalf("load config: %v", err)
	}

	dataset, err := evalpkg.LoadDataset(cfg.Eval.DatasetPath)
	if err != nil {
		fatalf("load eval dataset: %v", err)
	}

	client, err := llm.NewOpenAICompatibleClient(llm.OpenAICompatibleConfig{
		BaseURL: cfg.LLM.BaseURL,
		APIKey:  cfg.LLM.APIKey,
		Model:   cfg.LLM.Model,
		Timeout: cfg.LLM.Timeout,
	})
	if err != nil {
		fatalf("create LLM client: %v", err)
	}

	toolList := []tools.Tool{
		tools.CalculatorTool{},
		tools.OrderTool{},
		tools.ProductTool{},
	}

	var provider *observability.Provider
	if cfg.Observability.Enabled {
		provider, err = observability.NewProvider(ctx, observability.ProviderConfig{
			LangfuseBaseURL:    cfg.Observability.LangfuseBaseURL,
			OTLPTracesEndpoint: cfg.Observability.OTLPTracesEndpoint,
			PublicKey:          cfg.Observability.PublicKey,
			SecretKey:          cfg.Observability.SecretKey,
			ServiceName:        cfg.Observability.ServiceName,
			ServiceVersion:     cfg.Observability.ServiceVersion,
			ExportTimeout:      cfg.Observability.ExportTimeout,
		})
		if err != nil {
			fatalf("create observability provider: %v", err)
		}
	}

	var llmClient llm.Client = client
	if provider != nil {
		llmClient = observability.WrapLLM(client, provider.Tracer(), cfg.LLM.Model, cfg.Observability.CaptureContent)
		for i, tool := range toolList {
			toolList[i] = observability.WrapTool(tool, provider.Tracer(), cfg.Observability.CaptureContent)
		}
	}

	registry, err := tools.NewRegistry(toolList...)
	if err != nil {
		fatalf("create tool registry: %v", err)
	}

	baseAgent, err := agent.New(llmClient, registry, agent.Config{
		SystemPrompt: agent.DefaultSystemPrompt,
		MaxTurns:     cfg.Agent.MaxTurns,
		Temperature:  cfg.LLM.Temperature,
		MaxTokens:    cfg.LLM.MaxTokens,
		Debug:        cfg.Agent.Debug,
		DebugWriter:  os.Stderr,
	})
	if err != nil {
		fatalf("create agent: %v", err)
	}

	var scoreSink evalpkg.ScoreSink
	if cfg.Eval.UploadScores && provider != nil {
		scoreClient, err := langfuse.NewScoreClient(
			cfg.Observability.LangfuseBaseURL,
			cfg.Observability.PublicKey,
			cfg.Observability.SecretKey,
			cfg.Observability.ExportTimeout,
		)
		if err != nil {
			fatalf("create Langfuse score client: %v", err)
		}
		scoreSink = scoreClient
	}
	var datasetSynced bool
	if cfg.Eval.UploadDataset && provider != nil {
		datasetClient, err := langfuse.NewDatasetClient(
			cfg.Observability.LangfuseBaseURL,
			cfg.Observability.PublicKey,
			cfg.Observability.SecretKey,
			cfg.Observability.ExportTimeout,
		)
		if err != nil {
			fatalf("create Langfuse dataset client: %v", err)
		}

		items := make([]langfuse.DatasetItem, 0, len(dataset.Cases))
		for _, item := range dataset.Cases {
			itemMetadata := map[string]any{
				"case_id":    item.ID,
				"category":   item.Metadata.Category,
				"difficulty": item.Metadata.Difficulty,
			}
			if item.Metadata.EvaluationNote != "" {
				itemMetadata["evaluation_note"] = item.Metadata.EvaluationNote
			}
			items = append(items, langfuse.DatasetItem{
				DatasetName: cfg.Eval.DatasetName,
				ID:          item.ID,
				Input: map[string]any{
					"text": item.Input,
				},
				ExpectedOutput: item.Expected,
				Metadata:       itemMetadata,
			})
		}

		_, err = datasetClient.Sync(
			ctx,
			cfg.Eval.DatasetName,
			"Deterministic Phase 3 agent evaluation dataset",
			map[string]any{
				"phase":       "3",
				"source_path": cfg.Eval.DatasetPath,
				"evaluator":   "deterministic",
			},
			items,
		)
		if err != nil {
			fatalf("sync Langfuse dataset: %v", err)
		}
		datasetSynced = true
	}

	if provider != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Observability.ExportTimeout)
			defer cancel()
			if err := provider.Shutdown(shutdownCtx); err != nil {
				fmt.Fprintf(os.Stderr, "shutdown observability: %v\n", err)
			}
		}()
	}

	fmt.Println("Go Agent Eval - Phase 3")
	fmt.Printf("Run: %s\n", cfg.Eval.RunName)
	fmt.Printf("Dataset: %s (%d cases)\n", cfg.Eval.DatasetName, len(dataset.Cases))
	fmt.Printf("Dataset source: %s\n", cfg.Eval.DatasetPath)
	fmt.Printf("Langfuse tracing: %t\n", provider != nil)
	fmt.Printf("Langfuse dataset sync: %t\n", datasetSynced)
	fmt.Printf("Langfuse scores: %t\n\n", scoreSink != nil)

	startedAt := time.Now()
	runner := evalpkg.Runner{
		AgentForCase: func(item evalpkg.Case) agent.Runner {
			if provider == nil {
				return baseAgent
			}
			metadata := map[string]string{
				"app":        cfg.Observability.ServiceName,
				"phase":      "3",
				"eval_run":   cfg.Eval.RunName,
				"case_id":    item.ID,
				"category":   item.Metadata.Category,
				"difficulty": item.Metadata.Difficulty,
			}
			return observability.WrapAgent(
				baseAgent,
				provider.Tracer(),
				observability.TraceContext{
					TraceName:   cfg.Eval.TraceName,
					Environment: cfg.Eval.Environment,
					Version:     cfg.Observability.ServiceVersion,
					Metadata:    metadata,
				},
				cfg.Observability.CaptureContent,
			)
		},
		ScoreSink: scoreSink,
		Progress:  os.Stdout,
	}

	results, err := runner.Run(ctx, dataset)
	if err != nil {
		fatalf("run evaluation: %v", err)
	}

	if provider != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), cfg.Observability.ExportTimeout)
		if err := provider.ForceFlush(flushCtx); err != nil {
			fmt.Fprintf(os.Stderr, "flush observability: %v\n", err)
		}
		cancel()
	}

	report := evalpkg.BuildReport(cfg.Eval.RunName, cfg.Eval.DatasetPath, cfg.LLM.Model, startedAt, results)
	if err := evalpkg.SaveReport(cfg.Eval.ReportPath, report); err != nil {
		fatalf("save eval report: %v", err)
	}

	printSummary(report, cfg.Eval.ReportPath)
}

func printSummary(report evalpkg.Report, reportPath string) {
	summary := report.Summary
	fmt.Println("\n=== Evaluation Summary ===")
	fmt.Printf("cases: %d  passed: %d  failed: %d\n", summary.Cases, summary.Passed, summary.Failed)
	fmt.Printf("agent_errors: %d  score_upload_errors: %d\n", summary.AgentErrors, summary.ScoreUploadErrors)
	fmt.Println("\nmetrics:")
	for _, name := range evalpkg.SortedMetricNames(summary) {
		fmt.Printf("  %-28s %.4f\n", name, summary.MetricAverages[name])
	}
	fmt.Println("\ncategories:")
	for _, name := range evalpkg.SortedCategoryNames(summary) {
		category := summary.Categories[name]
		fmt.Printf("  %-22s cases=%2d task_success=%.4f\n", name, category.Cases, category.TaskSuccessAvg)
	}
	fmt.Printf("\nreport: %s\n", reportPath)
	fmt.Printf("completed: %s\n", report.CompletedAt.Format(time.RFC3339))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
