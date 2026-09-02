# 01 — 实现同步 ReAct Agent 的 Langfuse Trace 观测

**What to build:** 在现有 Langfuse OTLP 实验中加入一个确定性的同步 ReAct Agent。运行实验时，Agent 固定执行两轮：第一轮调用 `knowledge_search` 工具，工具返回固定知识片段，第二轮生成最终答案。Langfuse 中应能看到一次完整的 Agent 执行树，学习者可以据此观察 Agent、轮次、Generation、Tool 和 Retriever 之间的父子关系。

**Blocked by:** None — can start immediately

**Status:** resolved

- [x] Agent 执行创建 `agent.execute` Span，并在其下创建 `agent.round.1` 和 `agent.round.2` Span；正常路径在第二轮得到最终答案后结束。
- [x] 第一轮模型观测记录为 Generation，并表示工具调用意图；第二轮模型观测记录工具结果和最终答案。
- [x] `agent.tool.knowledge_search` 作为第一轮子 Span 创建，内部 `retrieve` 作为工具子 Span 创建。
- [x] 所有 Agent、轮次、Generation、工具和检索节点共享同一个 Trace ID，且各节点的 `langfuse.observation.type` 正确反映其角色。
- [x] Generation 包含模型名、Prompt、Completion、结束原因以及 input/output/total tokens 等关键属性。
- [x] 工具 Span 包含有界的工具输入、输出和成功或错误状态；工具使用固定数据，不访问真实模型、数据库或网络服务。
- [x] Agent 设置最大轮数；达到上限、工具输入无效或工具执行失败时返回错误，并结束已创建的 Span。
- [x] Agent 执行入口可以使用内存 Span Recorder 验证，不依赖 Langfuse 凭证或网络请求。
- [x] 测试验证最终答案、Span 树结构、父子关系、Trace ID 一致性、观测类型、关键 Generation 属性和错误路径。
- [x] 现有 JSON 队列 `traceparent` 序列化与恢复测试保持通过，不改变现有跨进程传播示例。
- [x] README 更新 Agent Trace 树、固定两轮行为、Span 类型、预期控制台现象和运行测试命令。
- [x] 不新增 Agent 注册表、通用 Tool 接口、模型适配层、数据库结构、HTTP API 或运行时配置项。
- [x] 完成后通过受影响实验的 `go test -race`、实验运行命令和 `go vet ./...` 验证；真实运行输出最终答案和 Trace ID。

## Answer

已在 `experiments/langfuse-otel` 实现固定两轮同步 ReAct Agent、完整 Langfuse 观测树、内存 exporter 测试和错误退出路径；保留原 JSON 队列传播示例。验证通过：`go test -race ./experiments/langfuse-otel`、`go run ./experiments/langfuse-otel`、`go vet ./...`、`go test ./...`。
