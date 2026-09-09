# Runtime 执行与组合层重构

## Problem Statement

当前 Agent 同时承担 LLM provider、工具注册表、单轮 LLM/tool loop、运行结果和会话执行接口。会话历史虽然已经独立为 Session，但两个命令入口仍分别完成 provider、工具、Session 和观测包装，组合逻辑存在重复。这个结构把执行机制、业务 policy 和应用组装混在一起，后续演进 runtime 时会迫使当前 Agent 接口和评估契约一起变化。

## Solution

采用 Pigo 的分层思想，将无状态执行 loop 迁移到 runtime，并让程序入口直接负责应用组装：`cmd/cli/main.go` 是交互式 CLI 入口，`cmd/eval/main.go` 是评估入口。两者使用已经解析完成的显式配置创建并复用 provider、工具注册表和 runtime，为每个交互会话或评估 case 创建独立的内存 Session。System prompt 由 policy 生成并由程序入口注入，观测作为 runtime 外部 decorator 保持在核心之外。

## User Stories

1. As a runtime maintainer, I want the LLM/tool execution loop to belong to runtime, so that Agent no longer owns orchestration mechanics.
2. As a runtime maintainer, I want the runtime loop to receive provider and tool dependencies explicitly, so that execution does not read process environment or construct hidden dependencies.
3. As an application maintainer, I want each executable entrypoint to own its composition, so that startup lifecycle and dependency wiring remain explicit.
4. As a caller, I want provider, Tool Registry, and runtime resources to be reused within one process, so that each new conversation does not repeat expensive setup.
5. As a caller, I want every conversation or evaluation case to receive a fresh Session, so that history from one conversation cannot leak into another.
6. As a Session owner, I want a successful turn to commit its new history, so that the next turn sees the completed conversation.
7. As a Session owner, I want a failed or cancelled turn to leave history unchanged, so that incomplete assistant or tool messages cannot corrupt later turns.
8. As a policy owner, I want system prompt construction to stay outside runtime, so that business instructions can change without changing execution mechanics.
9. As an observability owner, I want LLM, tool, and completed-run decoration to remain outside runtime, so that tracing does not create a dependency cycle with the execution core.
10. As an evaluator, I want to consume a runtime Runner, so that evaluation does not depend on a package named Agent or on a concrete loop implementation.
11. As a user of the current CLI, I want the synchronous RunResult behavior to remain unchanged, so that interactive and evaluation behavior does not change during the architecture migration.
12. As a caller, I want context cancellation to stop the runtime before continuing LLM or tool work, so that cancelled work does not append an incomplete turn.
13. As a runtime maintainer, I want maximum-turn and provider/tool errors to remain explicit, so that callers can distinguish a stopped run from a successful answer.
14. As a future maintainer, I want Session persistence to be replaceable outside the runtime loop, so that adding storage does not require changing execution control flow.
15. As a project maintainer, I want the existing evaluation Dataset to remain frozen during this migration, so that architecture changes are measured against stable smoke coverage rather than new business contracts.

## Implementation Decisions

- The runtime module owns the stateless execution Engine, synchronous LLM/tool loop, runtime configuration, run results, tool-call records, Runner contracts, and maximum-turn error.
- The LLM module continues to own provider-neutral messages, requests, responses, the existing client contract, and provider adapters.
- The tools module continues to own the Tool contract, Tool Registry, tool definitions, and concrete business tools.
- The runtime Engine receives the existing LLM client contract and concrete Tool Registry. No second runtime-only Provider or ToolExecutor interface is introduced while there is only one required implementation of each capability.
- `cmd/cli/main.go` 和 `cmd/eval/main.go` 各自消费显式、已经解析完成的配置。runtime 核心和执行 loop 不读取环境变量、不加载 dotenv，也不发现应用资源。
- 每个程序入口创建 provider 和工具集合，在注册前应用 LLM/Tool 装饰器，创建 Engine，注入 policy 生成的初始 history，并为新的 Session 返回可运行的 Runner。
- Provider、Tool Registry 和 Engine 资源在各自进程生命周期内创建一次并复用。每个交互会话和评估 case 都获得独立 Session。
- Session 仍是内存 history 的持有者。文件、数据库或其他持久化位于 runtime loop 和本次迁移之外。
- Policy 仍独立于 runtime。程序入口调用 policy 获取初始 history，而不是让 runtime 导入业务 policy。
- Observability remains an external decorator over the runtime Runner, LLM client, and Tool implementations. The runtime core must not import observability.
- The current history-in/history-out contract remains the execution seam: the Engine does not mutate caller-owned history, successful turns return the next history, and failed turns return no committable history.
- The loop remains synchronous and returns the existing result shape. Event streams, streaming UI contracts, steering, compaction, and event buses are not introduced.
- Agent 包保留无状态的 Agent 叶子适配器，负责把已组装的 runtime Engine 暴露给上层；`session` 包单独负责会话历史和 Prompt/Reset 便利接口。执行 loop、结果类型、Runner 契约、配置和最大轮次错误属于 runtime。旧的无状态 Agent loop 实现不重复保留。
- Observability and evaluation naming changes from Agent-specific contracts to runtime Runner contracts as part of the same clean cutover.
- The current 20-case Dataset is not expanded or semantically redesigned by this change. It remains a regression smoke set until the runtime contract stabilizes.

## Testing Decisions

- 测试跨越最高价值的 seam：Engine 的 history-in/history-out 行为、Session 的 Run 行为，以及各程序入口产生的最终 Runner。
- Tests assert externally observable behavior: successful history commit, failure rollback, cancellation propagation, maximum-turn termination, provider/tool invocation behavior, fresh-session isolation, and decorator compatibility.
- Tests do not assert private loop functions, field copying, constructor implementation, or incidental wiring order.
- Existing Agent and Session behavior tests are migrated to the public `agent.Agent` and `session.Session` seams and the runtime Engine rather than duplicated against private loop implementations.
- Existing observability and evaluation tests are updated to consume runtime Runner and result types.
- The implementation must run the repository-required Go formatting, targeted agent-trace tests, race tests, vet, and the existing local evaluation smoke command after the migration.

## Out of Scope

- Subagents, delegation trees, workflow graphs, task schedulers, and child-run protocols.
- Pigo-style EventStream, streaming provider APIs, steering hooks, compaction, reminders, or event buses.
- Session persistence, resume, fork, branch, JSONL storage, or database adapters.
- New business cases or Dataset expansion.
- LLM-as-a-Judge, groundedness scoring, CI quality gates, and new Langfuse evaluation semantics.
- New provider implementations or new concrete business tools.
- A generic dependency-injection framework, provider factory registry, or future-facing compatibility layer.

## Further Notes

本规格记录 `docs/adr/0001-runtime-execution-composition.md` 中的架构决定。Pigo 参考项目使用运行组装层准备 provider、工具注册表和运行配置，然后调用 runtime loop；Session 持久化由宿主驱动，而不是由 loop 直接负责。本项目将运行组装职责直接放在 `cmd/cli/main.go` 和 `cmd/eval/main.go`，不再增加独立的 runtime/bootstrap 或 cmd/cli/app 组装包。
