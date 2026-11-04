# Go 实验场

本项目是一个 Go 学习与验证代码库。实验彼此独立，分别用于观察 Go 语言、标准库、网络编程、并发、I/O、运行时以及可观测性等主题。

## 环境要求

- Go 1.26.6 或兼容版本
- 需要调用模型的实验还需要一个 OpenAI-compatible API
- 使用 Langfuse 的实验需要对应的访问凭证

## 实验目录

| 实验 | 内容 | 运行方式 |
| --- | --- | --- |
| [`agent-trace`](experiments/agent-trace) | 带计算器、订单和商品工具的多轮 Agent；支持 OpenAI-compatible API、OpenTelemetry/Langfuse Trace，以及本地评测和评分上传。 | `go run ./experiments/agent-trace/cmd/obs` |
| [`json-custom-struct`](experiments/json-custom-struct) | 根据 `role` 判别 JSON 消息类型，并验证缺少或不支持的消息类型。 | `go test ./experiments/json-custom-struct` |
| [`langfuse-otel`](experiments/langfuse-otel) | 使用固定输入演示 ReAct Agent 的 Span 层级、Generation/Tool/Retriever 观测类型和 W3C Trace Context 传播。 | `go run ./experiments/langfuse-otel` |

## 快速开始

在项目根目录执行：

```bash
go test ./...
go vet ./...
```

### 运行 `agent-trace`

复制示例配置：

```bash
cp experiments/agent-trace/.example.env experiments/agent-trace/.env
```

编辑 `.env`，至少设置 `LLM_BASE_URL` 和 `LLM_MODEL`；需要鉴权时设置 `LLM_API_KEY`。只运行本地 Agent 时，将 `LANGFUSE_ENABLED` 设为 `false`；启用 Langfuse 时还需要填写 `LANGFUSE_PUBLIC_KEY` 和 `LANGFUSE_SECRET_KEY`。

启动交互式 Agent：

```bash
go run ./experiments/agent-trace/cmd/obs
```

运行评测数据集：

```bash
go run ./experiments/agent-trace/cmd/eval
```

评测报告默认写入 `experiments/agent-trace/eval-results/latest.json`。该目录已被 Git 忽略；不要提交包含模型输入输出或凭证的本地产物。

### 运行 `langfuse-otel`

复制并填写 Langfuse 配置：

```bash
cp experiments/langfuse-otel/config.yaml.example experiments/langfuse-otel/config.yaml
```

然后运行：

```bash
go run ./experiments/langfuse-otel
```

不想使用默认配置路径时，可通过 `LANGFUSE_CONFIG` 指定配置文件。该实验不会调用真实大模型，运行结果中的 Trace ID 可用于在 Langfuse 控制台定位本次 Trace。

## 项目约定

- 每个实验保持局部性，不直接依赖其他实验。
- 优先使用标准库；实验专属依赖只在对应实验中使用。
- 可重复的行为、边界条件、错误路径和并发安全性应由测试验证。
- 配置文件、API 密钥和本地评测报告不提交到版本库。
