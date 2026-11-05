/**
 * 崩溃注入的子进程：按 STEP 走到设计的语义位置，然后对自己发 SIGKILL。
 *
 * 为什么必须真杀：进程内 throw 走的是"你的代码失败只影响自己的单元"那条规则，
 * 不会经过 durable 的崩溃计数、attempt 记账与恢复路径——那样测出来的是异常处理，
 * 不是持久化语义。parent 由 src/probes/p1.ts 负责重启并读存储。
 */
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { fauxAssistantMessage, fauxToolCall } from "@earendil-works/pi-ai";
import { agentOf, installAnswerScript, openHarness } from "../harness.ts";
import { registryWithSideEffect } from "../probes/tool.ts";

const step = process.env.STEP;
const dbPath = process.env.DB_PATH;
if (!step || !dbPath) throw new Error("需要 STEP 与 DB_PATH 环境变量");

// 工具类步骤需要一个装着夹具工具的注册表；重开时父进程必须用同一个定义。
const toolMode = step.startsWith("tool-");
const logPath = process.env.SIDE_EFFECT_LOG;
if (toolMode && !logPath) throw new Error("工具步骤需要 SIDE_EFFECT_LOG");
const restartLimit = process.env.RESTART_LIMIT;
const { harness, faux, close } = await openHarness({
	dbPath,
	...(restartLimit === undefined ? {} : { settings: { restartLimit: Number(restartLimit) } }),
	...(toolMode
		? {
				registry: registryWithSideEffect({
					logPath: logPath!,
					replay: process.env.REPLAY === "safe" ? "safe" : "unsafe",
					killEveryRun: process.env.KILL_EVERY === "1",
				}).registry,
			}
		: {}),
});
installAnswerScript(faux);
const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(faux) });

switch (step) {
	case "before-submit":
		// 断言 #1 的左半边：submit 之前死，输入根本不存在。
		process.kill(process.pid, "SIGKILL");
		break;

	case "after-submit": {
		// 断言 #1 的右半边：submit() 一旦 resolve，输入就已经落盘。
		await root.submit({ type: "input", content: "边界内的输入" }, BACKGROUND_CONTEXT);
		process.kill(process.pid, "SIGKILL");
		break;
	}

	case "atomic-commit": {
		// 断言 #2：commit 回调里先写一条 entry 再抛错，整批必须回滚。
		try {
			await root.commit(
				async (tx) => {
					await tx.appendEntry(root.id, { kind: "app.note", data: { probe: "atomic" } });
					throw new Error("commit 回调故意失败");
				},
				BACKGROUND_CONTEXT,
			);
		} catch {
			// 期望的回滚，继续往下走去崩溃。
		}
		process.kill(process.pid, "SIGKILL");
		break;
	}

	case "request-id": {
		// 断言 #3 的前半：输入以固定 requestId 入队后立刻崩溃。
		await root.submit(
			{ type: "input", content: "幂等输入", requestId: "probe-p1-request" },
			BACKGROUND_CONTEXT,
		);
		process.kill(process.pid, "SIGKILL");
		break;
	}

	case "tool-crash": {
		// 让模型调用工具；工具在 execute() 里写副作用，可选地自杀。
		faux.setResponses([
			fauxAssistantMessage([fauxToolCall("probe_side_effect", { note: "crash" })], { stopReason: "toolUse" }),
		]);
		await root.submit({ type: "input", content: "跑一次副作用" }, BACKGROUND_CONTEXT);
		// 若没被杀掉，说明注入点没命中，直接失败比静默继续好。
		await new Promise((resolve) => setTimeout(resolve, 5000));
		throw new Error("工具没有按预期崩溃");
	}

	case "tool-resume": {
		// 由父进程反复拉起：每次都从上次的 checkpoint 继续，工具会在 execute() 里再杀自己一次。
		harness.resume();
		await harness.waitForIdle(BACKGROUND_CONTEXT);
		break;
	}

	default:
		throw new Error(`未知 STEP: ${step}`);
}

// SIGKILL 之后不会执行到这里；close 只是让类型收窄与静态检查满意。
await close();
