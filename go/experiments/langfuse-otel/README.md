# Langfuse OTLP 实验

这个实验验证 Go 程序通过 OpenTelemetry SDK 将 Trace 发送到 Langfuse，包含确定性的同步 ReAct Agent 和模拟 Asynq 的跨进程传播。

## 链路

```text
experiment.request (chain)
├── agent.execute (agent)
│   ├── agent.round.1 (chain)
│   │   ├── agent.generation.1 (generation)
│   │   └── agent.tool.knowledge_search (tool)
│   │       └── retrieve (retriever)
│   └── agent.round.2 (chain)
│       └── agent.generation.2 (generation)
├── retrieve
├── generation
├── enqueue
└── worker.process
    └── generation.async
```

Agent 使用固定状态机：第一轮 Generation 请求 `knowledge_search`，工具返回固定知识片段；第二轮 Generation 读取该结果并生成最终答案。Agent 最多执行三轮，达到上限、工具输入无效或工具失败时返回错误并结束已创建的 Span。

`langfuse.observation.type` 分别使用 `agent`、`chain`、`generation`、`tool` 和 `retriever` 表示节点角色。所有 Agent 节点都从父 `context.Context` 创建，因此共享 `experiment.request` 的 Trace ID。

`enqueue` 将当前 W3C `traceparent` 写入 JSON；`worker.process` 从 JSON 恢复上下文。因此 Worker 不直接共享 Go 的 `context.Context`，但仍能加入原 Trace。

## 配置

默认读取 `experiments/langfuse-otel/config.yaml`。配置文件格式：

```yaml
base_url: "https://jp.cloud.langfuse.com"
public_key: "pk-lf-..."
secret_key: "sk-lf-..."
environment: "experiment"
```

仓库提供了 `config.yaml.example` 模板；真实 `config.yaml` 已加入 `.gitignore`，不要提交凭证。
也可以通过 `LANGFUSE_CONFIG=/path/to/config.yaml` 指定其他配置文件。

实验使用 OTLP/HTTP JSON，发送到：

```text
{base_url}/api/public/otel/v1/traces
```

认证使用 Langfuse Basic Auth，并设置 `x-langfuse-ingestion-version: 4`，让 v4 数据实时进入控制台。

## 依赖

- `go.opentelemetry.io/otel`、`otel/sdk`、`otel/trace` 与 `otlptracehttp` `v1.46.0`：创建、传播并通过 Langfuse 支持的 OTLP/HTTP 协议导出 Span；复用标准 OpenTelemetry 组件，避免维护自定义 Trace 协议和 exporter。
- `go.yaml.in/yaml/v3` `v3.0.5`：读取现有 YAML 凭证配置；保持配置可读且不把凭证写进源码。

## 运行

```bash
go test ./experiments/langfuse-otel
go test -race ./experiments/langfuse-otel
go run ./experiments/langfuse-otel
```

单元测试使用内存 Span exporter 验证 Agent 的结果、父子结构、Trace ID、属性和错误路径，不需要 Langfuse 凭证或网络。真实运行需要配置文件；输出包含固定最终答案和本次 Trace ID。程序退出时会 Shutdown `TracerProvider`，等待 BatchSpanProcessor 刷新数据。控制台打开 Langfuse 的 Traces 页面，查找 `Go OTLP experiment`。

## 说明

- Agent 和原有 `generation` 都使用固定的 `demo-model`，不调用真实 LLM，也不会产生模型费用。
- 两轮 Generation 记录 Prompt、Completion、结束原因以及 input/output/total token 属性。
- `knowledge_search` 使用固定短文本，不访问数据库、HTTP 或真实知识库。
- 现有 JSON 队列传播示例保持独立，没有把 Agent 轮次放入异步 Worker。
- 实验没有 Agent 注册表、通用 Tool 接口、模型适配层或运行时配置项。
