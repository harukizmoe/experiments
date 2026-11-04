package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	agent "harukizmoe/experiments/go/experiments/agent-trace/agent"
	config "harukizmoe/experiments/go/experiments/agent-trace/config"
	llm "harukizmoe/experiments/go/experiments/agent-trace/llm"
	observability "harukizmoe/experiments/go/experiments/agent-trace/observability"
	policy "harukizmoe/experiments/go/experiments/agent-trace/policy"
	runtime "harukizmoe/experiments/go/experiments/agent-trace/runtime"
	session "harukizmoe/experiments/go/experiments/agent-trace/session"
	tools "harukizmoe/experiments/go/experiments/agent-trace/tools"
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

// main 是交互式 CLI 的进程入口；run 负责配置加载、资源生命周期和交互循环。
func main() {
	if err := run(); err != nil {
		fatalf("%v", err)
	}
}

// run 在退出前关闭共享观测 Provider，并保持每个进程只有一个交互 Session。
func run() (returnErr error) {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := New(
		ctx,
		Config{
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
		},
	)
	if err != nil {
		return fmt.Errorf("create CLI runtime: %w", err)
	}

	provider := app.Provider()
	if provider != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				cfg.Observability.ExportTimeout,
			)

			defer cancel()

			if shutdownErr := app.Close(shutdownCtx); shutdownErr != nil {
				returnErr = errors.Join(
					returnErr,
					fmt.Errorf("shutdown observability: %w", shutdownErr),
				)
			}
		}()
	}

	runner, err := app.NewRunner(
		observability.TraceContext{
			TraceName:   cfg.Observability.TraceName,
			Environment: cfg.Observability.Environment,
			Version:     cfg.Observability.ServiceVersion,
			Metadata: map[string]string{
				"app":   cfg.Observability.ServiceName,
				"phase": "2",
			},
		},
	)
	if err != nil {
		return fmt.Errorf("create session runner: %w", err)
	}

	fmt.Println(
		"Go Agent CLI",
	)

	fmt.Printf(
		"Model: %s\n",
		cfg.LLM.Model,
	)

	fmt.Printf(
		"Base URL: %s\n",
		cfg.LLM.BaseURL,
	)

	fmt.Printf("Langfuse tracing: %t\n", cfg.Observability.Enabled)
	fmt.Println("Type /quit or /exit to leave.")
	fmt.Println()

	scanner := bufio.NewScanner(
		os.Stdin,
	)

	for {
		fmt.Print("> ")

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(
			scanner.Text(),
		)

		if input == "" {
			continue
		}

		if input == "/quit" ||
			input == "/exit" {
			break
		}

		result, runErr := runner.Run(
			ctx,
			input,
		)

		if provider != nil {
			flushCtx, cancel := context.WithTimeout(
				context.Background(),
				3*time.Second,
			)

			if err := app.ForceFlush(flushCtx); err != nil && cfg.Agent.Debug {
				fmt.Fprintf(
					os.Stderr,
					"[otel.flush.error] %v\n",
					err,
				)
			}

			cancel()
		}

		if runErr != nil {
			if errors.Is(
				runErr,
				runtime.ErrMaxTurnsExceeded,
			) {
				fmt.Fprintf(
					os.Stderr,
					"agent stopped: %v\n",
					runErr,
				)
			} else {
				fmt.Fprintf(
					os.Stderr,
					"agent error: %v\n",
					runErr,
				)
			}

			if result != nil &&
				result.Answer != "" {

				fmt.Println(
					result.Answer,
				)
			}

			continue
		}

		fmt.Println(
			result.Answer,
		)

		if cfg.Agent.Debug {
			fmt.Fprintf(
				os.Stderr,
				"[run.summary] turns=%d tool_calls=%d input_tokens=%d output_tokens=%d total_tokens=%d latency=%s trace_id=%s root_observation_id=%s\n",
				result.Turns,
				len(result.ToolCalls),
				result.Usage.InputTokens,
				result.Usage.OutputTokens,
				result.Usage.TotalTokens,
				result.Latency,
				result.TraceID,
				result.RootObservationID,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
