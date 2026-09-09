# 将 Agent Loop 迁移到 Runtime、依赖组装迁移到 CLI

当前 `agent` 同时承担 LLM/tool loop、运行结果和会话执行接口，`cmd/cli/main.go` 与 `cmd/eval/main.go` 还分别完成 provider、tool registry、Engine 和 Session 的组装。决定采用 Pigo 的分层思想：`runtime` 核心只负责无状态同步 loop、生命周期和结果模型；`llm` 与 `tools` 保持独立能力包；由各程序入口使用显式配置创建并复用 provider、Tool Registry、Engine，再为每个会话创建独立的内存 Session。Policy 由程序入口注入，Session 持久化留在宿主层，observability 作为外部 decorator；暂不引入 EventStream、子 Agent 或其他未来扩展抽象。

## Considered Options

- 让 runtime 核心直接读取环境变量并创建具体 provider、业务工具和观测组件：绑定当前实验，且容易形成循环依赖，拒绝。
- 引入独立的 runtime/bootstrap 或 cmd/cli/app 共享组装包：会把程序启动责任再次隐藏在间接层，拒绝。

## Consequences

- `runtime` 保留无状态 loop；`agent` 包保留无状态的 Agent 叶子适配器；`session` 包单独负责会话历史、Prompt、Run 和 Reset，不创建 Provider 或工具。
- provider、Tool Registry 和 Engine 在各程序入口的进程生命周期内复用；每个评估 case 或交互会话拥有独立 history。
- 当前同步 `RunResult` 契约保持不变，现有 Dataset 仅作为回归烟雾集，不因这次架构调整扩充。
