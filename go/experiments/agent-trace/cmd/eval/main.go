package main

import (
	"context"
	"errors"
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
	policy "harukizmoe/go-exp/experiments/agent-trace/policy"
	runtime "harukizmoe/go-exp/experiments/agent-trace/runtime"
	session "harukizmoe/go-exp/experiments/agent-trace/session"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

// Config 是已经由宿主解析完成的命令运行配置。
// 程序入口组合层不读取环境变量，也不负责 dotenv 加载。
type Config struct {
	LLM           llm.OpenAICompatibleConfig
	Runtime       runtime.Config
	Observability ObservabilityConfig
}

// ObservabilityConfig 控制 Provider 创建以及 LLM、Tool 和 Runner 装饰器。
type ObservabilityConfig struct {
	Enabled        bool
	Provider       observability.ProviderConfig
	CaptureContent bool
}

// App 持有命令生命周期内共享的 Provider 和 Runtime Engine；Engine 持有 Tool Registry。
// 每次调用 NewRunner 都会创建一个拥有独立历史的 Session。
type App struct {
	engine         *runtime.Engine
	initialHistory []llm.Message
	provider       *observability.Provider
	captureContent bool
}

// New 创建共享的 LLM client、Tool Registry 和 Runtime Engine。
// 当启用观测时，Provider 以及 LLM、Tool 装饰器也在这里创建；调用方必须最终调用 Close。
func New(ctx context.Context, cfg Config) (app *App, err error) {
	client, err := llm.NewOpenAICompatibleClient(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	var provider *observability.Provider

	// Provider 构造或后续 Registry、Engine 初始化失败时，不能遗留 OTLP exporter。
	defer func() {
		if err == nil || provider == nil {
			return
		}

		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			providerShutdownTimeout(cfg.Observability.Provider.ExportTimeout),
		)
		defer cancel()

		if shutdownErr := provider.Shutdown(shutdownCtx); shutdownErr != nil {
			err = errors.Join(
				err,
				fmt.Errorf("shutdown observability after CLI composition failure: %w", shutdownErr),
			)
		}
	}()

	if cfg.Observability.Enabled {
		provider, err = observability.NewProvider(ctx, cfg.Observability.Provider)
		if err != nil {
			return nil, fmt.Errorf("create observability provider: %w", err)
		}
	}

	var llmClient llm.Client = client
	toolList := []tools.Tool{
		tools.CalculatorTool{},
		tools.OrderTool{},
		tools.ProductTool{},
		tools.OrderDetailsTool{},
		tools.ReturnPolicyTool{},
	}

	if provider != nil {
		tracer := provider.Tracer()
		llmClient = observability.WrapLLM(
			client,
			tracer,
			cfg.LLM.Model,
			cfg.Observability.CaptureContent,
		)

		for i, tool := range toolList {
			toolList[i] = observability.WrapTool(
				tool,
				tracer,
				cfg.Observability.CaptureContent,
			)
		}
	}

	registry, err := tools.NewRegistry(toolList...)
	if err != nil {
		return nil, fmt.Errorf("create tool registry: %w", err)
	}

	engine, err := runtime.NewEngine(llmClient, registry, cfg.Runtime)
	if err != nil {
		return nil, fmt.Errorf("create runtime engine: %w", err)
	}

	return &App{
		engine:         engine,
		initialHistory: policy.InitialHistory(),
		provider:       provider,
		captureContent: cfg.Observability.CaptureContent,
	}, nil
}

// NewRunner 为一个新的交互会话或评估 case 创建独立 Session。
// Provider 存在时，返回值还会被 Runner 观测装饰器包装。
func (app *App) NewRunner(traceContext observability.TraceContext) (runtime.Runner, error) {
	executor, err := agent.New(app.engine)
	if err != nil {
		return nil, fmt.Errorf("create agent: %w", err)
	}

	conversation, err := session.New(executor, app.initialHistory)
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	if app.provider == nil {
		return conversation, nil
	}

	return observability.WrapRunner(
		conversation,
		app.provider.Tracer(),
		traceContext,
		app.captureContent,
	), nil
}

// Provider 返回命令生命周期内共享的观测 Provider；未启用观测时返回 nil。
func (app *App) Provider() *observability.Provider {
	return app.provider
}

// ForceFlush 等待当前已创建的观测 Span 尽可能导出。
func (app *App) ForceFlush(ctx context.Context) error {
	if app == nil || app.provider == nil {
		return nil
	}
	return app.provider.ForceFlush(ctx)
}

// Close 释放命令生命周期内共享的观测 Provider。
func (app *App) Close(ctx context.Context) error {
	if app == nil || app.provider == nil {
		return nil
	}
	return app.provider.Shutdown(ctx)
}

func providerShutdownTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return 10 * time.Second
	}
	return timeout
}

func main() {
	if err := run(); err != nil {
		fatalf("%v", err)
	}
}

func run() (runErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dataset, err := evalpkg.LoadDataset(cfg.Eval.DatasetPath)
	if err != nil {
		return fmt.Errorf("load eval dataset: %w", err)
	}

	app, err := New(ctx, Config{
		LLM: llm.OpenAICompatibleConfig{
			BaseURL: cfg.LLM.BaseURL,
			APIKey:  cfg.LLM.APIKey,
			Model:   cfg.LLM.Model,
			Timeout: cfg.LLM.Timeout,
		},
		Runtime: runtime.Config{
			MaxTurns:    cfg.Agent.MaxTurns,
			Temperature: cfg.LLM.Temperature,
			MaxTokens:   cfg.LLM.MaxTokens,
			Debug:       cfg.Agent.Debug,
			DebugWriter: os.Stderr,
		},
		Observability: ObservabilityConfig{
			Enabled: cfg.Observability.Enabled,
			Provider: observability.ProviderConfig{
				LangfuseBaseURL:    cfg.Observability.LangfuseBaseURL,
				OTLPTracesEndpoint: cfg.Observability.OTLPTracesEndpoint,
				PublicKey:          cfg.Observability.PublicKey,
				SecretKey:          cfg.Observability.SecretKey,
				ServiceName:        cfg.Observability.ServiceName,
				ServiceVersion:     cfg.Observability.ServiceVersion,
				ExportTimeout:      cfg.Observability.ExportTimeout,
			},
			CaptureContent: cfg.Observability.CaptureContent,
		},
	})
	if err != nil {
		return fmt.Errorf("create CLI runtime: %w", err)
	}

	provider := app.Provider()
	if provider != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Observability.ExportTimeout)
			defer cancel()

			if shutdownErr := app.Close(shutdownCtx); shutdownErr != nil {
				runErr = errors.Join(
					runErr,
					fmt.Errorf("shutdown observability: %w", shutdownErr),
				)
			}
		}()
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
			return fmt.Errorf("create Langfuse score client: %w", err)
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
			return fmt.Errorf("create Langfuse dataset client: %w", err)
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
			input := map[string]any{
				"text": item.Input,
			}
			if len(item.FollowUps) > 0 {
				input["follow_ups"] = item.FollowUps
			}
			items = append(items, langfuse.DatasetItem{
				DatasetName:    cfg.Eval.DatasetName,
				ID:             item.ID,
				Input:          input,
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
			return fmt.Errorf("sync Langfuse dataset: %w", err)
		}
		datasetSynced = true
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
		RunnerForCase: func(item evalpkg.Case) (runtime.Runner, error) {
			metadata := map[string]string{
				"app":        cfg.Observability.ServiceName,
				"phase":      "3",
				"eval_run":   cfg.Eval.RunName,
				"case_id":    item.ID,
				"category":   item.Metadata.Category,
				"difficulty": item.Metadata.Difficulty,
			}
			return app.NewRunner(
				observability.TraceContext{
					TraceName:   cfg.Eval.TraceName,
					Environment: cfg.Eval.Environment,
					Version:     cfg.Observability.ServiceVersion,
					Metadata:    metadata,
				},
			)
		},
		ScoreSink: scoreSink,
		Progress:  os.Stdout,
	}

	results, err := runner.Run(ctx, dataset)
	if err != nil {
		return fmt.Errorf("run evaluation: %w", err)
	}

	if provider != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), cfg.Observability.ExportTimeout)
		flushErr := app.ForceFlush(flushCtx)
		cancel()
		if flushErr != nil {
			return fmt.Errorf("flush observability: %w", flushErr)
		}
	}

	report := evalpkg.BuildReport(cfg.Eval.RunName, cfg.Eval.DatasetPath, cfg.LLM.Model, startedAt, results)
	if err := evalpkg.SaveReport(cfg.Eval.ReportPath, report); err != nil {
		return fmt.Errorf("save eval report: %w", err)
	}

	printSummary(report, cfg.Eval.ReportPath)
	return nil
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
