# 同步 ReAct Agent 的 Langfuse Trace 观测

Status: ready-for-agent

## Problem Statement

当前 Langfuse OTLP 实验已经验证了普通 Span、Generation，以及 JSON 队列边界上的 W3C `traceparent` 传播，但还没有表现真实 Agent 的多轮推理和工具调用结构。学习者无法通过当前实验直观看到一次 Agent 执行如何在 Langfuse 中形成 `agent.execute → agent.round.N → Generation / tool` 的观测树。

本实验仍应保持独立、可重复、低成本。它不应依赖真实大模型、外部知识库或业务系统，否则 Trace 结构学习会被网络、凭证和模型响应的不确定性干扰。

## Solution

在现有 Langfuse OTLP 实验中增加一个确定性的同步 ReAct Agent。Agent 使用 Fake Model 固定执行两轮：第一轮请求 `knowledge_search` 工具，第二轮根据工具结果生成最终答案。

Agent 的每次执行都创建一个 `agent.execute` Span；每轮创建一个 `agent.round.N` Span；模型调用记录为 Generation；工具调用记录为 Tool Span，检索步骤作为其子 Span。所有节点复用同一条 OpenTelemetry Trace，并由现有 TracerProvider 批量发送到 Langfuse。

第一阶段只验证 Agent 的同步观测树。现有 JSON 队列传播实验继续独立验证跨进程 Context 传播，不把 Asynq、真实 LLM 和 Agent 逻辑同时引入。

## User Stories

1. 作为 Go 初学者，我希望运行一个不调用真实大模型的 Agent 实验，以便零模型费用地学习 Langfuse Trace。
2. 作为 Go 初学者，我希望看到一次 Agent 执行对应一个 `agent.execute` Span，以便理解 Agent 执行边界。
3. 作为 Go 初学者，我希望看到每一轮 ReAct 推理对应一个 `agent.round.N` Span，以便理解多轮 Agent 行为如何映射到 Trace。
4. 作为 Go 初学者，我希望第一轮模型调用被记录为 Generation，以便观察模型输入、工具调用意图和 Token 使用量。
5. 作为 Go 初学者，我希望模型请求工具时能看到对应的 Tool Span，以便区分模型决策和工具执行。
6. 作为 Go 初学者，我希望工具内部的检索步骤作为 Tool Span 的子 Span 出现，以便观察更细粒度的操作层级。
7. 作为 Go 初学者，我希望第二轮模型调用能读取工具结果并生成最终答案，以便观察 Agent 从工具结果回到模型的完整链路。
8. 作为 Trace 使用者，我希望所有 Agent、轮次、Generation 和工具节点拥有相同的 Trace ID，以便在 Langfuse 中定位一次完整 Agent 执行。
9. 作为 Trace 使用者，我希望每个节点的 `langfuse.observation.type` 能反映其角色，以便 Langfuse 正确呈现 Agent、Generation、Tool 和 Retriever。
10. 作为 Trace 使用者，我希望 Generation 包含模型名、Prompt、Completion、结束原因和 Token 数，以便分析模型调用内容和消耗。
11. 作为 Trace 使用者，我希望工具 Span 包含工具名称、输入、输出和执行结果，以便排查 Agent 是否正确使用工具。
12. 作为实验维护者，我希望 Fake Model 每次都返回相同的两轮结果，以便测试和 Langfuse 控制台中的观察结果稳定可复现。
13. 作为实验维护者，我希望 Agent 设置最大轮数，以便错误状态不会造成无法退出的循环。
14. 作为实验维护者，我希望工具失败时仍然结束已创建的 Span 并返回错误，以便错误链路不会留下未结束节点。
15. 作为实验维护者，我希望通过内存 Span Recorder 验证 Trace 树，而不是让单元测试访问 Langfuse 网络，以便测试快速、隔离且不消耗外部资源。
16. 作为实验维护者，我希望保留现有队列传播测试，以便 Agent Trace 实验不会削弱已经验证的跨边界传播契约。
17. 作为实验维护者，我希望不引入新的第三方依赖，以便实验继续遵守标准库和现有依赖优先的约定。
18. 作为使用者，我希望运行实验后仍能获得最终答案和 Trace ID，以便在 Langfuse 控制台中定位本次执行。
19. 作为使用者，我希望 README 说明新的 Agent Trace 树、运行方式和预期现象，以便按照实验步骤复现观察结果。
20. 作为凭证管理员，我希望 Agent 实验继续复用现有 YAML 配置和忽略规则，以便凭证不进入 Go 源码或版本库。

## Implementation Decisions

- 修改现有 Langfuse OTLP 实验，不创建新的独立实验目录；这样可以复用现有配置读取、OTLP/HTTP exporter、Provider 生命周期和队列传播示例。
- 在现有请求根 Span 下增加同步 Agent 执行边界。请求根仍负责代表一次完整实验，Agent 执行作为其子节点。
- 在 Agent 执行边界提供一个可测试的执行入口，由调用方传入 `context.Context` 和 `trace.Tracer`。生产运行使用现有全局 Tracer；测试使用内存 Span Recorder 对应的 Tracer。该入口是本功能的唯一主要测试 seam。
- Fake Model 使用固定状态机：第一轮返回一个 `knowledge_search` 工具调用；工具返回固定知识片段；第二轮返回固定最终答案。工具调用参数和结果使用有界 JSON 或短文本，避免把无关数据写入 Trace。
- Agent 最多执行三轮；本次正常路径只使用两轮。得到最终答案后立即结束循环；达到上限或遇到错误时结束当前 Span 并返回错误。
- `agent.execute` 使用 `agent` 类型；`agent.round.N` 使用 `chain` 类型；`agent.tool.knowledge_search` 使用 `tool` 类型；工具内部检索使用 `retriever` 类型；模型调用使用 `generation` 类型。
- 每轮 Generation 是该轮 Span 的子节点。工具 Span 是触发工具调用的轮次 Span 的子节点，检索 Span 是工具 Span 的子节点。所有子节点都从父节点返回的 Context 创建，不手工拼接 Trace ID 或 Observation ID。
- Generation 记录 `gen_ai.operation.name`、`gen_ai.request.model`、Prompt、Completion、结束原因以及 input/output/total tokens，并保留现有 Langfuse GenAI 属性命名方式。
- Tool Span 记录工具名称、有限长度的输入和输出，以及成功或错误状态。工具执行不依赖数据库、HTTP 或真实检索系统。
- 不增加 Agent 注册表、通用 Tool 接口、模型适配层或配置项；当前只有一个 Fake Model 和一个工具，增加抽象不会产生真实复用收益。
- 现有 JSON `traceparent` 队列传播流程保持不变，并继续作为独立的跨进程传播演示；本功能第一阶段不把 Agent 轮次拆到异步 Worker。
- README 增加 Agent Trace 树、两轮状态机、Span 类型、预期控制台现象和测试命令。现有 YAML 配置格式和 `LANGFUSE_CONFIG` 覆盖方式不变。
- 不新增数据库 schema、HTTP API 或运行时环境变量。

## Testing Decisions

- 好的测试应验证外部可观察行为：最终答案、错误退出、Span 名称、父子关系、Trace ID 一致性、观测类型和关键 Generation 属性；不验证局部变量、函数调用次数或具体实现布局。
- 在 Agent 执行 seam 上使用 OpenTelemetry SDK 的内存 Span Recorder，调用确定性的 Fake Model 和工具。测试不得发送网络请求，也不得依赖 Langfuse 凭证。
- 正常路径测试验证以下可观察结构：`agent.execute` 包含两轮；第一轮包含模型 Generation 和 `agent.tool.knowledge_search`；工具下包含 `retrieve`；第二轮包含最终 Generation；所有节点共享 Trace ID。
- 正常路径测试还验证第一轮 Generation 具有工具调用结果，第二轮 Generation 具有最终答案和 Token 属性，工具和检索节点具有正确的 observation type。
- 错误路径测试覆盖缺少或无效工具输入，以及达到最大轮数；测试应确认返回错误且相关 Span 已结束，不产生无法回收的执行路径。
- 继续运行现有的 JSON 队列传播测试，确保本次 Agent 观测扩展没有破坏 `traceparent` 序列化、恢复和缺失字段错误处理。
- 测试风格参考现有实验测试：使用标准 `testing` 包、隔离的 SDK Provider、显式 Shutdown，以及表驱动或子测试表达边界行为。
- 完成实现后运行受影响实验的 `go test -race`、实验运行命令和 `go vet ./...`；真实运行只用于确认 OTLP 数据可以发送，不把远端控制台作为单元测试依赖。

## Out of Scope

- 真实 OpenAI、Anthropic、Ollama 或其他模型调用。
- 真实向量数据库、知识库、RAG 检索或网络工具。
- Asynq 中间件、异步 Agent Worker 或新的跨进程传播协议。
- 流式模型响应、Time-To-First-Token 和流式 Span 更新。
- 多工具注册表、并行工具调用、工具重试和复杂 Agent 编排框架。
- Prompt 版本管理、Langfuse Dataset、在线评分和 LLM-as-a-judge。
- Agent 业务鉴权、用户会话持久化和生产级取消策略。
- WeKnora 其他模型类型，包括 embedding、rerank、VLM 和 ASR 的新增模拟。
- 新的配置字段、环境变量、HTTP API 或数据库结构。
- 对现有队列传播示例进行架构重构。

## Further Notes

- 该设计刻意把“Agent 观测树”和“跨进程传播”拆成两个可独立观察的实验维度。先稳定同步 ReAct Trace，再考虑将工具或 Agent 轮次放入异步队列。
- Langfuse 控制台中的最终显示依赖 OTLP ingestion 和当前属性映射；本地测试只保证发送的 Span 结构和属性契约。
- 观测输入和输出应保持短小且不包含凭证、个人信息或无界模型上下文。
- 实现完成后应以一次真实运行得到的 Trace ID 作为人工观察入口，但不把该 ID 固化到测试或文档中。
