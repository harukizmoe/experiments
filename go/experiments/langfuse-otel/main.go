// 本实验演示 Go 程序如何通过 OpenTelemetry SDK 把 Trace 发送到 Langfuse。
//
// 学习重点有三个：
//  1. 用 Span 表达同步 ReAct Agent 的多轮执行树；
//  2. 用 Generation、Tool 和 Retriever 类型区分观测角色；
//  3. 用 W3C traceparent 在模拟的队列边界传递 Trace 上下文。
//
// 这里不调用真实大模型，固定数据只用于观察 Langfuse 的 Generation 映射，
// 因此运行实验不会产生模型调用费用。
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"go.yaml.in/yaml/v3"
)

// 这些名称会同时出现在 OpenTelemetry 的 instrumentation 和 Langfuse 界面中。
const (
	serviceName = "langfuse-otel-experiment"
	tracerName  = "goexp/langfuse"
)

// Fake Model 和工具数据都是实验内的稳定输入；固定值保证运行与测试可重复。
const (
	// fakeModelName 作为两轮 Generation 的模型标识，不对应真实模型服务。
	fakeModelName = "demo-model"
	// maxAgentRounds 是异常状态机的退出上限；正常路径只执行两轮。
	maxAgentRounds = 3
	// defaultToolInput、knowledgeResult 和 finalAnswer 描述固定的工具调用与回答链路。
	defaultToolInput = `{"query":"Go context cancellation"}`
	knowledgeResult  = "context.Context 在 Go 调用链中传递取消信号和截止时间。"
	finalAnswer      = "Go 使用 context.Context 在调用链中传递取消信号和截止时间。"
)

// defaultConfigPath 是默认配置文件路径；真实凭证放在被 .gitignore 忽略的 config.yaml 中。
const defaultConfigPath = "experiments/langfuse-otel/config.yaml"

// langfuseConfig 描述连接 Langfuse 所需的最小配置。
// YAML 字段使用 snake_case，与配置文件中的字段保持一致。
type langfuseConfig struct {
	// BaseURL 是 Langfuse 实例根地址；exporter 会在其后追加 OTLP Trace 路径。
	BaseURL string `yaml:"base_url"`
	// PublicKey 和 SecretKey 分别作为 Langfuse Basic Auth 的用户名和密码。
	PublicKey string `yaml:"public_key"`
	SecretKey string `yaml:"secret_key"`
	// Environment 标识 Trace 所属环境，空值在加载时归一为 experiment。
	Environment string `yaml:"environment"`
}

// tracePayload 是“队列消息”的最小示例。
//
// 真实任务通常还会包含业务字段；这里仅保留 traceparent，突出跨进程传播的核心。
// traceparent 是 W3C Trace Context 标准定义的字符串。
type tracePayload struct {
	// Traceparent 保存发送方当前 Span 的传播信息。
	Traceparent string `json:"traceparent"`
}

// main 只负责把运行结果映射为进程退出码；run 会在返回前完成 Provider 清理。
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 组织实验生命周期：加载配置、初始化 exporter、运行实验、最后刷新并关闭 Provider。
func run() error {
	// Background 是本次独立实验的根 Context；真实 HTTP 服务通常从请求 Context 开始。
	ctx := context.Background()

	// 配置文件与代码分离，避免凭证出现在 Go 源码中。
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Provider 管理 Span 的创建、批量缓存和导出。
	provider, err := newTracerProvider(ctx, config)
	if err != nil {
		return fmt.Errorf("create tracer provider: %w", err)
	}

	// defer 保证成功或失败返回前都会尝试把缓存中的 Span 发出去。
	defer func() {
		// Shutdown 使用独立 Context，不能依赖可能已经取消的业务 Context。
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := provider.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "shutdown tracer provider: %v\n", err)
		}
	}()

	// runExperimentWithAnswer 返回根 Trace ID 和 Fake Model 的最终答案，便于在控制台定位本次实验。
	traceID, answer, err := runExperimentWithAnswer(ctx)
	if err != nil {
		return fmt.Errorf("run experiment: %w", err)
	}
	fmt.Printf("agent answer: %s\ntrace created: %s\n", answer, traceID)
	return nil
}

// loadConfig 从 YAML 文件读取 Langfuse 配置，并清理配置值首尾的空格。
func loadConfig() (langfuseConfig, error) {
	// LANGFUSE_CONFIG 允许切换配置文件；默认路径适合本实验直接运行。
	path := envOr("LANGFUSE_CONFIG", defaultConfigPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return langfuseConfig{}, fmt.Errorf("read %s: %w", path, err)
	}

	// yaml.Unmarshal 根据结构体标签把 YAML 字段映射为 Go 字段。
	var config langfuseConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return langfuseConfig{}, fmt.Errorf("parse %s: %w", path, err)
	}
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	config.PublicKey = strings.TrimSpace(config.PublicKey)
	config.SecretKey = strings.TrimSpace(config.SecretKey)
	config.Environment = strings.TrimSpace(config.Environment)
	if config.Environment == "" {
		config.Environment = "experiment"
	}
	return config, nil
}

// newTracerProvider 创建 OpenTelemetry SDK 的 TracerProvider，并把 OTLP/HTTP exporter 接到 Langfuse。
//
// SDK 已经处理 Span ID、父子关系、批量发送和关闭时刷新；实验代码只关注在哪里开始和结束 Span。
func newTracerProvider(ctx context.Context, config langfuseConfig) (*sdktrace.TracerProvider, error) {
	// 配置来自 YAML；缺少任一凭证时直接失败，避免发出未认证请求。
	if config.PublicKey == "" || config.SecretKey == "" {
		return nil, errors.New("public_key and secret_key are required in config")
	}

	// BaseURL 表示 Langfuse 实例根地址，例如 https://jp.cloud.langfuse.com。
	host := strings.TrimRight(config.BaseURL, "/")
	if host == "" {
		host = "https://cloud.langfuse.com"
	}

	// Langfuse OTLP 接口使用 Basic Auth，用户名和密码分别是 Public/Secret Key。
	auth := base64.StdEncoding.EncodeToString([]byte(config.PublicKey + ":" + config.SecretKey))
	exporter, err := otlptracehttp.New(
		ctx,
		otlptracehttp.WithEndpointURL(host+"/api/public/otel/v1/traces"),
		otlptracehttp.WithEncoding(otlptracehttp.EncodingJSON),
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization":                "Basic " + auth,
			"x-langfuse-ingestion-version": "4",
		}),
	)
	if err != nil {
		// exporter 创建失败表示 endpoint 或客户端配置无效，应在程序启动时暴露错误。
		return nil, fmt.Errorf("create OTLP HTTP exporter: %w", err)
	}

	// Resource 描述“哪个服务”产生了 Span，会出现在 Langfuse 的资源元数据中。
	res, err := resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			attribute.String("deployment.environment", config.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	// BatchSpanProcessor 不在每次 End 时立即发 HTTP，而是先批量缓存，减少网络请求次数。
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	// 全局 propagator 负责把 Context 编码成 traceparent，也负责从 traceparent 解码回来。
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return provider, nil
}

// reactAgent 是本实验唯一的固定 Agent 状态机；字段仅用于测试错误退出路径。
// 正常运行由 newReactAgent 提供固定的三轮上限和工具输入。
type reactAgent struct {
	// maxRounds 限制状态机轮数，防止模型持续请求工具时无限循环。
	maxRounds int
	// toolInput 是第一轮传给 knowledge_search 的有界 JSON。
	toolInput string
	// failTool 只用于确定性验证工具失败时的 Span 生命周期。
	failTool bool
}

// newReactAgent 返回确定性 Agent：第一轮调用工具，第二轮生成最终答案。
func newReactAgent() reactAgent {
	return reactAgent{maxRounds: maxAgentRounds, toolInput: defaultToolInput}
}

// execute 创建 Agent、轮次、Generation、Tool 和 Retriever 的父子观测树。
// 每轮从 agentCtx 创建，因此两个轮次都是 agent.execute 的直接子节点。
func (agent reactAgent) execute(ctx context.Context, tracer trace.Tracer) (string, error) {
	agentCtx, agentSpan := tracer.Start(ctx, "agent.execute", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "agent"),
		attribute.Int("agent.max_rounds", agent.maxRounds),
	))
	defer agentSpan.End()

	fail := func(err error) (string, error) {
		agentSpan.SetStatus(codes.Error, err.Error())
		agentSpan.SetAttributes(attribute.String("langfuse.observation.error", err.Error()))
		return "", err
	}
	if agent.maxRounds <= 0 {
		return fail(errors.New("maximum rounds must be positive"))
	}

	toolResult := ""
	for round := 1; round <= agent.maxRounds; round++ {
		roundCtx, roundSpan := tracer.Start(agentCtx, fmt.Sprintf("agent.round.%d", round), trace.WithAttributes(
			attribute.String("langfuse.observation.type", "chain"),
			attribute.Int("agent.round.number", round),
		))

		prompt, completion, finishReason, inputTokens, outputTokens, totalTokens, wantsTool := agent.modelResponse(round, toolResult)
		_, generation := tracer.Start(roundCtx, fmt.Sprintf("agent.generation.%d", round), trace.WithAttributes(
			attribute.String("langfuse.observation.type", "generation"),
			attribute.String("gen_ai.system", "demo"),
			attribute.String("gen_ai.operation.name", "chat"),
			attribute.String("gen_ai.request.model", fakeModelName),
			attribute.String("gen_ai.prompt", prompt),
			attribute.String("gen_ai.completion", completion),
			attribute.StringSlice("gen_ai.response.finish_reasons", []string{finishReason}),
			attribute.Int("gen_ai.usage.input_tokens", inputTokens),
			attribute.Int("gen_ai.usage.output_tokens", outputTokens),
			attribute.Int("gen_ai.usage.total_tokens", totalTokens),
		))
		generation.End()

		if !wantsTool {
			roundSpan.SetAttributes(attribute.String("langfuse.observation.output", finalAnswer))
			roundSpan.End()
			agentSpan.SetAttributes(attribute.String("langfuse.observation.output", finalAnswer))
			return finalAnswer, nil
		}

		var err error
		toolResult, err = agent.runKnowledgeSearch(roundCtx, tracer)
		if err != nil {
			roundSpan.SetStatus(codes.Error, err.Error())
			roundSpan.SetAttributes(attribute.String("langfuse.observation.error", err.Error()))
			roundSpan.End()
			return fail(err)
		}
		roundSpan.End()
	}

	return fail(fmt.Errorf("maximum rounds reached: %d", agent.maxRounds))
}

// modelResponse 是 Fake Model 的固定两状态响应，不访问模型服务。
func (agent reactAgent) modelResponse(round int, toolResult string) (string, string, string, int, int, int, bool) {
	if round == 1 {
		return `[{"role":"user","content":"What does Go context.Context do?"}]`,
			`{"tool":"knowledge_search","input":` + agent.toolInput + `}`, "tool_calls", 18, 12, 30, true
	}
	return `[{"role":"user","content":"What does Go context.Context do?"},{"role":"tool","content":"` + toolResult + `"}]`,
		`[{"role":"assistant","content":"` + finalAnswer + `"}]`, "stop", 32, 16, 48, false
}

// runKnowledgeSearch 记录固定工具和其内部检索；输入、输出均保持短小且有界。
func (agent reactAgent) runKnowledgeSearch(ctx context.Context, tracer trace.Tracer) (string, error) {
	toolCtx, toolSpan := tracer.Start(ctx, "agent.tool.knowledge_search", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "tool"),
		attribute.String("tool.name", "knowledge_search"),
		attribute.String("langfuse.observation.input", limitText(agent.toolInput, 256)),
	))
	defer toolSpan.End()

	var input struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(agent.toolInput), &input); err != nil {
		return agent.toolError(toolSpan, fmt.Errorf("tool input is invalid JSON: %w", err))
	}
	if strings.TrimSpace(input.Query) == "" {
		return agent.toolError(toolSpan, errors.New("tool input is empty"))
	}
	if agent.failTool {
		return agent.toolError(toolSpan, errors.New("knowledge search failed"))
	}

	_, retrieveSpan := tracer.Start(toolCtx, "retrieve", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "retriever"),
		attribute.String("langfuse.observation.input", limitText(input.Query, 128)),
		attribute.String("langfuse.observation.output", limitText(knowledgeResult, 512)),
	))
	retrieveSpan.End()
	toolSpan.SetAttributes(
		attribute.String("langfuse.observation.output", knowledgeResult),
		attribute.String("tool.status", "success"),
	)
	return knowledgeResult, nil
}

func (agent reactAgent) toolError(span trace.Span, err error) (string, error) {
	span.SetStatus(codes.Error, err.Error())
	span.SetAttributes(
		attribute.String("tool.status", "error"),
		attribute.String("langfuse.observation.error", err.Error()),
		attribute.String("langfuse.observation.output", limitText(err.Error(), 512)),
	)
	return "", err
}

func limitText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

// runExperimentWithAnswer 构造观测树，并返回根 Trace ID 和最终答案。
func runExperimentWithAnswer(ctx context.Context) (trace.TraceID, string, error) {
	// Tracer 是创建 Span 的入口；tracerName 标识这段 instrumentation 代码。
	tracer := otel.Tracer(tracerName)

	// 根 Span 代表一次完整请求；从 ctx 创建的子 Span 会自动继承它的 Trace ID。
	ctx, root := tracer.Start(
		ctx, "experiment.request",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("langfuse.observation.type", "chain"),
			attribute.String("langfuse.trace.name", "Go OTLP experiment"),
			attribute.String("langfuse.user.id", "experiment-user"),
			attribute.String("langfuse.session.id", "experiment-session"),
			attribute.String("langfuse.release", "local"),
			attribute.StringSlice("langfuse.trace.tags", []string{"go", "otel", "experiment"}),
			attribute.String("langfuse.trace.metadata.mode", "minimal"),
		),
	)

	// 先保存 Trace ID，再结束 root；这样调用方仍能打印本次 Trace 的身份。
	traceID := root.SpanContext().TraceID()
	// root.End 必须执行，否则 exporter 不会收到完整的结束时间。
	defer func() {
		root.SetAttributes(attribute.String("langfuse.observation.output", "experiment completed"))
		root.End()
	}()
	answer, err := newReactAgent().execute(ctx, tracer)
	if err != nil {
		return traceID, "", fmt.Errorf("execute agent: %w", err)
	}

	// retrieve 是普通业务步骤，不是模型调用，所以用 retriever 类型标记。
	_, retrieval := tracer.Start(ctx, "retrieve", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "retriever"),
		attribute.String("langfuse.observation.input", `{"query":"1 + 1 = ?"}`),
		attribute.String("langfuse.observation.output", `{"documents":1}`),
	))
	// End 表示检索已经完成；Span 生命周期应覆盖真实工作的开始到结束。
	retrieval.End()

	// generation 模拟一次聊天模型调用。
	// gen_ai.* 是 OpenTelemetry 的 GenAI 属性，Langfuse 会据此识别模型和 Token 用量。
	_, generation := tracer.Start(ctx, "generation", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "generation"),
		attribute.String("gen_ai.system", "demo"),
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.request.model", "demo-model"),
		attribute.String("gen_ai.prompt", `[{"role":"user","content":"1 + 1 = ?"}]`),
		attribute.Int("gen_ai.usage.input_tokens", 12),
	))
	generation.SetAttributes(
		attribute.StringSlice("gen_ai.response.finish_reasons", []string{"stop"}),
		attribute.Int("gen_ai.usage.output_tokens", 8),
		attribute.Int("gen_ai.usage.total_tokens", 20),
		attribute.String("gen_ai.completion", `[{"role":"assistant","content":"2"}]`),
	)
	// 结束 Generation 后，Langfuse 会组合时间和属性展示模型调用详情。
	generation.End()

	// enqueue 模拟“把任务放进队列”；它此时仍属于当前请求 Trace。
	ctx, enqueue := tracer.Start(ctx, "enqueue", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "span"),
		attribute.String("queue.name", "demo-worker"),
	))
	payload, err := marshalTracePayload(ctx)
	// 先结束 enqueue，再把 payload 交给 worker，模拟跨进程边界已经发生。
	enqueue.End()
	if err != nil {
		return traceID, answer, fmt.Errorf("marshal trace payload: %w", err)
	}
	if err := runWorker(payload); err != nil {
		return traceID, answer, fmt.Errorf("run worker: %w", err)
	}
	return traceID, answer, nil
}

// runWorker 模拟另一个进程中的任务处理函数。
// 它不接收原始 Context，而是只接收序列化后的 JSON 消息。
func runWorker(payload []byte) error {
	// 从队列消息恢复 Context；恢复后的 Span 会成为 worker.process 的父级。
	workerCtx, err := unmarshalTraceContext(payload)
	if err != nil {
		return fmt.Errorf("restore worker context: %w", err)
	}

	// worker.process 代表异步任务本身，会自动挂在 enqueue 之后。
	tracer := otel.Tracer(tracerName)
	workerCtx, worker := tracer.Start(workerCtx, "worker.process", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "tool"),
		attribute.String("queue.name", "demo-worker"),
	))
	// defer 确保 worker 的所有返回路径都会设置结束时间。
	defer worker.End()

	// 这个 Generation 验证异步 Worker 创建的模型观测仍在原 Trace 中。
	_, generation := tracer.Start(workerCtx, "generation.async", trace.WithAttributes(
		attribute.String("langfuse.observation.type", "generation"),
		attribute.String("gen_ai.system", "demo"),
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.request.model", "demo-model"),
		attribute.String("gen_ai.prompt", `[{"role":"user","content":"async question"}]`),
		attribute.String("gen_ai.completion", `[{"role":"assistant","content":"async answer"}]`),
		attribute.Int("gen_ai.usage.input_tokens", 5),
		attribute.Int("gen_ai.usage.output_tokens", 7),
		attribute.Int("gen_ai.usage.total_tokens", 12),
	))
	generation.End()

	// 返回 nil 表示模拟任务成功；真实 Worker 会返回业务错误供队列重试。
	return nil
}

// marshalTracePayload 把当前 Context 中的传播信息编码成 JSON 队列消息。
func marshalTracePayload(ctx context.Context) ([]byte, error) {
	// MapCarrier 是 OpenTelemetry 提供的键值载体，行为类似 HTTP Header。
	carrier := propagation.MapCarrier{}
	// Inject 从 Context 取出当前 Span，并写入 carrier["traceparent"]。
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	// 没有 traceparent 通常说明调用方没有从 Span Context 开始，应尽早报错。
	traceparent := carrier.Get("traceparent")
	if traceparent == "" {
		return nil, errors.New("traceparent was not injected")
	}
	return json.Marshal(tracePayload{Traceparent: traceparent})
}

// unmarshalTraceContext 完成队列边界的另一半：JSON 解码后恢复父 Span Context。
func unmarshalTraceContext(payload []byte) (context.Context, error) {
	// 先把字节还原成普通 Go 结构体；真实队列通常也先做这一步。
	var message tracePayload
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, fmt.Errorf("decode trace payload: %w", err)
	}
	if message.Traceparent == "" {
		return nil, errors.New("traceparent is missing")
	}

	// Extract 不会创建新 Span，只把传播信息放回 Context，供后续 Start 使用。
	carrier := propagation.MapCarrier{"traceparent": message.Traceparent}
	return otel.GetTextMapPropagator().Extract(context.Background(), carrier), nil
}

// envOr 读取可选环境变量，并在没有配置时提供默认值。
func envOr(name, fallback string) string {
	// TrimSpace 处理 shell 配置中意外出现的首尾空格。
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
