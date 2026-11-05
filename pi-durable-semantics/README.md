# pi-durable 语义实验台

## 目的

把 [`@earendil-works/pi-durable`](https://github.com/earendil-works/pi/tree/main/packages/durable)（1.5.0）文档里的语义断言，变成**能失败的 probe**，并给出一屏能直接看见提交、恢复、重放的实验台。

学的是运行时语义，不是产品功能：这一版不做助理 bot，只做"输入 → 提交 → 崩溃 → 重启 → 续跑"这条链上可以观测的事实。

## 待验证的问题

| # | 断言（来自 README / 类型定义） | 判定方式 | 状态 |
| --- | --- | --- | --- |
| 1 | 提交先于可见：watch 不会先于提交暴露状态 | 提交前 `SIGKILL`，重启后 transcript 里不应出现该输入 | 待测 |
| 2 | `commit()` 原子：回调抛错则全部回滚 | 回调里先 append 再 throw，存储不应留半个 entry | 待测 |
| 3 | 重复 `requestId` 幂等 | 跨重启用同一 id submit，只能有一个 submission | 待测 |
| 4 | deferred commit：watch 立刻看到，存储晚到 | `withinMs` 内杀进程，这段内容应丢失 | 待测 |
| 5 | phase 内部无进度，重跑从顶部 | 工具里先写文件再杀，副作用应重复 | 待测 |
| 6 | 默认不重跑工具，模型收到 `interrupted` | 同上但不声明 replay | 待测 |
| 7 | `replay: "safe"` 会重跑且不保证幂等 | 声明后重跑，副作用计数 +1 | 待测 |
| 8 | `restartLimit` 默认 5 | 每次都在 checkpoint 前自杀，第 6 次应结束任务 | 待测 |
| 9 | 重启后任务状态是 `pending` 而非 `running` | 执行中杀进程，重启读任务图 | 待测 |
| 10 | 睡眠是状态，不占 invocation | `sleepUntil` 期间观察任务状态与进程存活 | 待测 |
| 11 | 假时钟可推进，跨重启按 `until` 立即执行 | 注入 `now`/`setTimer` | 待测 |
| 12 | inbox 落位顺序（steer / followUp / write / reject） | busy 时依次提交，看顺序与异常 | 待测 |
| 13 | watch 窗口 50/100 的 splice 与 gap | 连写 120 条，记录每次回调窗口 | 待测 |
| 14 | abort 自下而上级联 | 父任务持有子任务，abort 父 | 待测 |
| 15 | 崩溃中的模型请求算失败尝试并按 retry 重发 | 流中间 `SIGKILL`，看 `pi.live.generation.retry` 与最终回答 | **已验**（见结论） |

范围围栏：不测 provider 差异（全程 faux）、不测 Cloudflare DO 的 alarm/heartbeat、不测 codemode / MCP / 图片处理。

## 关键概念

- **Harness**：一个存储 + 跑任务的机器。所有变更走同一条原子提交线，先落盘再可见。
- **Conversation / Entry / Document**：会话是只增的 entry 记录；文档（`pi.live`、`pi.inbox`、`pi.usage`、`pi.agent`…）是和 transcript 一起原子提交的类型化 JSON。
- **Task / phase / checkpoint**：任务是有状态的机器，每个 phase 结束提交一次 checkpoint；**重启从 checkpoint 重跑整个 phase**，phase 内部不保留进度。
- **Submission / run / turn**：`submit()` 把输入durable 地入队；一次输入到答案之间是 run，一次模型响应加工具调用是 turn。
- **replay 策略**：工具声明 `replay: "safe"` 才会在崩溃后重跑；否则模型收到 `interrupted` 错误结果。重跑不等于幂等，副作用要自己做幂等键。
- **视图**：`watch()` 给出窗口化结构视图（默认最近 50 条，超过 100 条一次性 splice 到 50）；`ConversationTranscript` 把它拼成客户端整条记录并标出 gap。

## 运行命令

```bash
cd pi-durable-semantics
npm install --ignore-scripts

# P0：faux 接线 + 一次提交（自检，不依赖 UI）
npm run probe:p0

# 回归测试（P0 语义 + 视图快照 + import map 覆盖）
npm test

# 实验台：http://localhost:7788
npm start                 # 监护进程；子进程被 kill 后自动重启
FAUX_TPS=12 npm start     # 让流慢到能点中 kill（默认 12 token/秒）
DB_PATH=out/session.sqlite PORT=7788 npm start
```

按钮含义：`短消息` 提一句输入；`长消息（可打断）` 用一段长文本把流拉长；`kill -9` 直接 `SIGKILL` 承载 Harness 的进程（监护进程立即重启）；`重启` 走正常 `close()`；`压缩` 触发一次手动 compaction。

## 预期现象

- `npm run probe:p0` 输出 `pi.user -> pi.assistant`、`faux.callCount 1`、usage 非空，最后 `P0 OK`。
- 打开发验台后：左列是 transcript，右侧面板随 tab 切换 `Live / Inbox / Task graph / Usage`，底部是本进程的事件计数。
- 点 `长消息` → 流式输出出现在 Live；在流中间点 `kill -9`：
  - 页面短暂掉线（灯变红），随后自动重连；
  - 徽标出现 **RESTARTED**，Live 里出现 `generation.retry`，`attempt` 变为 2；
  - 重试完成后 transcript 里出现一条完整回答，崩溃前已提交的部分回答作为单独 entry 留在原地。
- 全程没有页面报错；`customElements.get("md-filled-button")` 必须有值（否则是 import map 漏项，页面白屏）。

## 结论

**已实测（1.5.0，2026-11）**

| 观察 | 结果 | 与文档一致？ |
| --- | --- | --- |
| faux 能否穿过 Harness | 能。`models.setProvider(faux.provider)` + `{ provider: "faux", modelId: "faux-1" }` | 文档未直接给 Harness 例子，实测成立 |
| 一次输入产生几次模型调用 | 1 次；entries 恰为 `pi.user → pi.assistant` | 一致 |
| assistant entry 由谁写入 | generation 任务自己写，host 不需要手动 `commit(AssistantEntry)` | 与 Quick Start 示例不同（示例多写了一步） |
| 进程被 `SIGKILL` 后存储 | transcript / usage / 文档全部保留，重启后 watch 直接给出窗口 | 一致 |
| 崩溃中的模型请求 | 重启后 `pi.live.generation.retry.error = "The host stopped while the model was answering, on attempt 1"`，attempt 2 完成 | 一致 |
| 半个回答去哪了 | 崩溃前已提交的部分作为独立 entry 保留，重试回答另起一条 | 一致 |
| 默认 retry | `enabled: true, maxRetries: 3, baseDelayMs: 2000`（取自 `dist/harness/agent.js`） | 一致 |
| watch 窗口默认值 | `min 50 / max 100`（取自 `dist/harness/view.d.ts`），splice 行为待测 | 待测 |

**踩到的坑（比结论更值钱）**

1. **faux 的脚本队列活在进程内存里**。第一次做崩溃实验时，脚本用完就没了，重启后的重试直接拿到 `model_error: No more faux responses queued` —— 这不是 durable 的缺陷，而是"外部模型不会随我们的进程一起消失"这个前提在测试夹具里没被建模。解法是 `installAnswerScript()`：工厂每次被取用时把自己重新排回队尾。**任何用 faux 测恢复的实验都必须先解决这一点**，否则测到的是夹具的死亡。
2. **import map 漏一项就是白屏**：`@material/web → lit → lit-element/lit-element.js`，而 `lit-element` 又是 via `lit-html → @lit-labs/ssr-dom-shim`。这些都不在 `package.json` 的直接依赖里，服务端也照样 200，只有浏览器报 `does not resolve to a valid URL`。因此加了一条静态断言扫描 vendor 里的裸模块名。
3. **视图与 Harness 必须同进程**：`watch()` 反映的是本进程的提交，`readOnly` 只保证"打开时不恢复、拒绝写入"，并不会跨进程推送别人的提交 [INFERENCE]。想跨进程实时只能走 chord 的 link —— 那条路留给产品，不在本实验。

**待验证**：上表 1–14 全部未测。当前只完成了 P0（faux 接线）与 U0（一屏 + 崩溃重启可见），这两步是后续 probe 的地基。
