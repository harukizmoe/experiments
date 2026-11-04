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

	agent "harukizmoe/go-exp/experiments/agent-trace/agent"
	config "harukizmoe/go-exp/experiments/agent-trace/config"
	llm "harukizmoe/go-exp/experiments/agent-trace/llm"
	observability "harukizmoe/go-exp/experiments/agent-trace/observability"
	tools "harukizmoe/go-exp/experiments/agent-trace/tools"
)

func main() {
	if err := run(); err != nil {
		fatalf("%v", err)
	}
}

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

	client, err := llm.NewOpenAICompatibleClient(
		llm.OpenAICompatibleConfig{
			BaseURL: cfg.LLM.BaseURL,
			APIKey:  cfg.LLM.APIKey,
			Model:   cfg.LLM.Model,
			Timeout: cfg.LLM.Timeout,
		},
	)
	if err != nil {
		return fmt.Errorf("create LLM client: %w", err)
	}

	toolList := []tools.Tool{
		tools.CalculatorTool{},
		tools.OrderTool{},
		tools.ProductTool{},
		tools.OrderDetailsTool{},
		tools.ReturnPolicyTool{},
	}

	var provider *observability.Provider
	if cfg.Observability.Enabled {
		provider, err = observability.NewProvider(
			ctx,
			observability.ProviderConfig{
				LangfuseBaseURL:    cfg.Observability.LangfuseBaseURL,
				OTLPTracesEndpoint: cfg.Observability.OTLPTracesEndpoint,
				PublicKey:          cfg.Observability.PublicKey,
				SecretKey:          cfg.Observability.SecretKey,
				ServiceName:        cfg.Observability.ServiceName,
				ServiceVersion:     cfg.Observability.ServiceVersion,
				ExportTimeout:      cfg.Observability.ExportTimeout,
			},
		)
		if err != nil {
			return fmt.Errorf("create observability provider: %w", err)
		}

		// Provider 创建成功后立即注册清理，覆盖后续初始化和交互运行失败路径。
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(
				context.Background(),
				cfg.Observability.ExportTimeout,
			)

			defer cancel()

			if shutdownErr := provider.Shutdown(shutdownCtx); shutdownErr != nil {
				returnErr = errors.Join(
					returnErr,
					fmt.Errorf("shutdown observability: %w", shutdownErr),
				)
			}
		}()
	}

	// Use interface variables so decorators can wrap
	// the concrete client/tool types.
	var llmClient llm.Client = client

	if cfg.Observability.Enabled {
		llmClient = observability.WrapLLM(
			client,
			provider.Tracer(),
			cfg.LLM.Model,
			cfg.Observability.CaptureContent,
		)

		for i, tool := range toolList {
			toolList[i] = observability.WrapTool(
				tool,
				provider.Tracer(),
				cfg.Observability.CaptureContent,
			)
		}
	}

	registry, err := tools.NewRegistry(
		toolList...,
	)
	if err != nil {
		return fmt.Errorf(
			"create tool registry: %w",
			err,
		)
	}

	baseAgent, err := agent.New(
		llmClient,
		registry,
		agent.Config{
			SystemPrompt: agent.DefaultSystemPrompt,
			MaxTurns:     cfg.Agent.MaxTurns,
			Temperature:  cfg.LLM.Temperature,
			MaxTokens:    cfg.LLM.MaxTokens,
			Debug:        cfg.Agent.Debug,
			DebugWriter:  os.Stderr,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"create agent: %w",
			err,
		)
	}

	var runner agent.Runner = baseAgent

	if cfg.Observability.Enabled {
		runner = observability.WrapAgent(
			baseAgent,
			provider.Tracer(),
			observability.TraceContext{
				TraceName:   cfg.Observability.TraceName,
				Environment: cfg.Observability.Environment,
				Version:     cfg.Observability.ServiceVersion,
				Metadata: map[string]string{
					"app":   cfg.Observability.ServiceName,
					"phase": "2",
				},
			},
			cfg.Observability.CaptureContent,
		)
	}

	fmt.Println(
		"Go Agent Eval - Phase 2",
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

			if err := provider.ForceFlush(
				flushCtx,
			); err != nil &&
				cfg.Agent.Debug {

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
				agent.ErrMaxTurnsExceeded,
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
