/**
 * P2：工具的 replay 策略与 restartLimit。
 *
 * 探针全部用真崩溃：工具在 execute() 里对自己 SIGKILL，所以崩点精确落在
 * "intent 已提交、结果未提交" 这个窗口——正是 replay 语义唯一有意义的位置。
 *
 * 运行：node --experimental-strip-types src/probes/p2.ts
 */
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import type { HarnessSettings, ToolDiagnostic } from "@earendil-works/pi-durable";
import { agentOf, installAnswerScript, openHarness } from "../harness.ts";
import { registryWithSideEffect } from "./tool.ts";

const STEP = join(import.meta.dirname, "..", "crash", "step.ts");
const TASK_TIMEOUT_MS = 15_000;

export interface ProbeResult {
	readonly id: string;
	readonly claim: string;
	readonly observed: string;
	readonly ok: boolean;
}

/** 跑一个子进程；返回它的退出信号（SIGKILL 表示按设计崩了，null 表示正常退出）。 */
function runStep(
	step: string,
	env: Record<string, string>,
): Promise<{ signal: NodeJS.Signals | null; code: number | null; stderr: string }> {
	const { promise, resolve, reject } = Promise.withResolvers<{
		signal: NodeJS.Signals | null;
		code: number | null;
		stderr: string;
	}>();
	const child = spawn(process.execPath, ["--experimental-strip-types", STEP], {
		env: { ...process.env, STEP: step, ...env },
		stdio: ["ignore", "pipe", "pipe"],
	});
	let stderr = "";
	child.stderr.on("data", (chunk) => (stderr += String(chunk)));
	child.on("error", (error) => reject(error));
	child.on("exit", (code, signal) => resolve({ signal, code, stderr }));
	return promise;
}

async function sideEffectLines(logPath: string): Promise<number> {
	try {
		const text = await readFile(logPath, "utf8");
		return text.split("\n").filter((line) => line.length > 0).length;
	} catch {
		return 0;
	}
}

/** 重新打开同一个库，装上同一份工具定义（崩溃后必须能解析到定义，否则任务 blocked）。 */
async function reopen(dbPath: string, logPath: string, replay: "safe" | "unsafe", settings?: HarnessSettings) {
	const opened = await openHarness({
		dbPath,
		registry: registryWithSideEffect({ logPath, replay, killEveryRun: false }).registry,
		...(settings === undefined ? {} : { settings }),
	});
	installAnswerScript(opened.faux);
	const root = await opened.harness.root(BACKGROUND_CONTEXT, { agent: agentOf(opened.faux) });
	return { ...opened, root };
}

/** 读 transcript 里所有 tool-result 的诊断码。 */
async function diagnosticCodes(root: Awaited<ReturnType<typeof reopen>>["root"]): Promise<string[]> {
	const page = await root.entries({ order: "ascending" }, 50, undefined, BACKGROUND_CONTEXT);
	return page.items
		.filter((entry) => entry.kind === "pi.tool-result")
		.flatMap((entry) => ((entry.data as { diagnostics?: ToolDiagnostic[] } | undefined)?.diagnostics ?? []).map((d) => d.code));
}

/** 等 harness 空闲；超时返回 "timeout" 而不是把整个 probe 卡死。 */
async function settle(harness: Awaited<ReturnType<typeof reopen>>["harness"]): Promise<string> {
	const { promise, resolve } = Promise.withResolvers<string>();
	const timer = setTimeout(() => resolve("timeout"), TASK_TIMEOUT_MS);
	await harness.waitForIdle(BACKGROUND_CONTEXT);
	clearTimeout(timer);
	resolve("idle");
	return promise;
}

export async function runP2(): Promise<ProbeResult[]> {
	const results: ProbeResult[] = [];

	// P2-1：默认（unsafe）——崩溃后工具不重跑，模型拿到 interrupted 错误结果。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p2a-"));
		const dbPath = join(dir, "s.sqlite");
		const logPath = join(dir, "effects.log");
		const crashed = await runStep("tool-crash", { DB_PATH: dbPath, SIDE_EFFECT_LOG: logPath, KILL_EVERY: "1" });
		const afterCrash = await sideEffectLines(logPath);

		const { harness, close, root } = await reopen(dbPath, logPath, "unsafe");
		// 断言 #9：重启后还没 resume 时，任务应显示 pending 而不是 running。
		const inspection = await harness.inspect(BACKGROUND_CONTEXT);
		const statusesBeforeResume = inspection.tasks.map((task) => task.state.kind).join(",");
		const outcome = await settle(harness);
		const afterResume = await sideEffectLines(logPath);
		const codes = await diagnosticCodes(root);

		results.push({
			id: "P2-1",
			claim: "默认策略下崩溃的工具不重跑，模型收到 interrupted",
			observed: `崩溃前=${afterCrash} 次副作用，resume 后=${afterResume} 次；诊断=${codes.join(",") || "无"}；${outcome}`,
			ok: crashed.signal === "SIGKILL" && afterCrash === 1 && afterResume === 1 && codes.includes("interrupted"),
		});
		results.push({
			id: "P2-9",
			claim: "重启后未 resume 时任务状态是 pending",
			observed: `tasks=${statusesBeforeResume || "空"}`,
			ok: statusesBeforeResume !== "" && !statusesBeforeResume.includes("running"),
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// P2-2：replay: "safe" —— 崩溃后工具重跑，副作用翻倍（重跑 ≠ 幂等）。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p2b-"));
		const dbPath = join(dir, "s.sqlite");
		const logPath = join(dir, "effects.log");
		await runStep("tool-crash", {
			DB_PATH: dbPath,
			SIDE_EFFECT_LOG: logPath,
			KILL_EVERY: "1",
			REPLAY: "safe",
		});
		const afterCrash = await sideEffectLines(logPath);

		const { harness, close } = await reopen(dbPath, logPath, "safe");
		const outcome = await settle(harness);
		const afterResume = await sideEffectLines(logPath);

		results.push({
			id: "P2-2",
			claim: "replay: safe 会在恢复时重跑工具，副作用重复",
			observed: `崩溃前=${afterCrash} 次，resume 后=${afterResume} 次；${outcome}`,
			ok: afterCrash === 1 && afterResume === 2,
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// P2-3：restartLimit —— 每次都崩的工具最终被放弃，而不是无限重启。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p2c-"));
		const dbPath = join(dir, "s.sqlite");
		const logPath = join(dir, "effects.log");
		const limit = 2;
		const env = { DB_PATH: dbPath, SIDE_EFFECT_LOG: logPath, KILL_EVERY: "1", REPLAY: "safe" };

		// 先制造第一个待恢复的 run（工具写到第 1 行后自杀），再反复 resume 让它继续崩。
		const first = await runStep("tool-crash", { ...env, RESTART_LIMIT: String(limit) });
		let crashes = first.signal === "SIGKILL" ? 1 : 0;
		let cleanExit = false;
		for (let attempt = 0; attempt < 6; attempt += 1) {
			const run = await runStep("tool-resume", { ...env, RESTART_LIMIT: String(limit) });
			if (run.signal === "SIGKILL") crashes += 1;
			if (run.signal === null) {
				cleanExit = true;
				break;
			}
		}

		const { harness, close, root } = await reopen(dbPath, logPath, "safe", { restartLimit: limit });
		await settle(harness);
		const lines = await sideEffectLines(logPath);
		const codes = await diagnosticCodes(root);
		const entries = await root.entries({ order: "ascending" }, 50, undefined, BACKGROUND_CONTEXT);

		results.push({
			id: "P2-3",
			claim: "restartLimit 用满后任务被放弃（restart_limit），不会无限重启",
			observed: `崩溃 ${crashes} 次后干净退出=${cleanExit}；副作用 ${lines} 次；诊断=${codes.join(",") || "无"}；entries=${entries.items.length}`,
			ok: cleanExit && lines === limit && codes.includes("restart_limit"),
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	return results;
}

if (import.meta.main) {
	const results = await runP2();
	for (const result of results) {
		console.log(`${result.ok ? "PASS" : "FAIL"}  ${result.id}  ${result.claim}\n      ${result.observed}`);
	}
	if (results.some((result) => !result.ok)) process.exit(1);
}
