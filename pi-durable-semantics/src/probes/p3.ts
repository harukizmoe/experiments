/**
 * P3：睡眠与假时钟、跨重启的到期唤醒、以及 deferred commit 的丢失窗口。
 *
 * 这里的关键是"时间"必须由宿主提供：Harness 的 `now` 与 `setTimer` 同时注入假时钟，
 * 睡眠的截止时刻才会存在可控的时间轴上；否则测到的是真实 setTimeout 的行为。
 *
 * 运行：node --experimental-strip-types src/probes/p3.ts
 */
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { agentOf, installAnswerScript, openHarness } from "../harness.ts";
import { ProbeDoc, SleepTask, fakeClock, registryWithSleep } from "./fixtures.ts";

const STEP = join(import.meta.dirname, "..", "crash", "step.ts");
const T0 = 1_000_000; // 任意固定起点：假时钟下不需要真实时间
const SLEEP_FOR = 60_000;

export interface ProbeResult {
	readonly id: string;
	readonly claim: string;
	readonly observed: string;
	readonly ok: boolean;
}

async function lines(path: string): Promise<number> {
	try {
		return (await readFile(path, "utf8")).split("\n").filter((line) => line.length > 0).length;
	} catch {
		return 0;
	}
}

/** 轮询等一个条件成立；用来等调度器把任务推进到目标状态，而不是盲等固定时长。 */
async function waitFor(check: () => Promise<boolean>, timeoutMs = 5000): Promise<boolean> {
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		if (await check()) return true;
		await new Promise((resolve) => setTimeout(resolve, 20));
	}
	return false;
}

/** inspect() 的 state 用 `kind` 而不是 TaskState 的 `status`；两者不要混用。 */
async function kinds(harness: Awaited<ReturnType<typeof openHarness>>["harness"]): Promise<string[]> {
	const inspection = await harness.inspect(BACKGROUND_CONTEXT);
	return inspection.tasks.map((task) => task.state.kind);
}

export async function runP3(): Promise<ProbeResult[]> {
	const results: ProbeResult[] = [];

	// P3-1 / P3-2：睡眠是持久状态，不占 invocation；推进假时钟才唤醒。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p3a-"));
		const dbPath = join(dir, "s.sqlite");
		const logPath = join(dir, "sleep.log");
		const clock = fakeClock(T0);
		const opened = await openHarness({
			dbPath,
			registry: registryWithSleep(),
			now: clock.now,
			setTimer: clock.setTimer,
		});
		installAnswerScript(opened.faux);
		const { harness, close } = opened;
		const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(opened.faux) });

		await harness.commit(
			(tx) =>
				tx.createTask(SleepTask, { at: T0 + SLEEP_FOR, logPath }, {
					ownership: { kind: "conversation" },
					conversationId: root.id,
				}),
			BACKGROUND_CONTEXT,
		);
		harness.resume();
		const slept = await waitFor(async () => (await kinds(harness)).includes("sleeping"));

		const sleepingState = (await harness.inspect(BACKGROUND_CONTEXT)).tasks.find(
			(task) => task.state.kind === "sleeping",
		)?.state as { until?: number } | undefined;
		const wake = harness.wake();
		const deliveredAnyway = await lines(logPath);
		results.push({
			id: "P3-10",
			claim: "睡眠是持久状态：无 invocation、无副作用，wake() 给出下一时刻",
			observed: `状态化为 sleeping=${slept}，until=${sleepingState?.until}，wake=${JSON.stringify(wake)}，deliver 次数=${deliveredAnyway}`,
			ok:
				slept &&
				sleepingState?.until === T0 + SLEEP_FOR &&
				wake.active === false &&
				wake.at === T0 + SLEEP_FOR &&
				deliveredAnyway === 0,
		});

		clock.advanceTo(T0 + SLEEP_FOR);
		const delivered = await waitFor(async () => (await lines(logPath)) === 1);
		results.push({
			id: "P3-11",
			claim: "推进假时钟到 until 才执行 deliver phase",
			observed: `deliver 次数=${await lines(logPath)}，等待成功=${delivered}`,
			ok: delivered,
		});

		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// P3-3：睡眠跨越重启——首次 Harness 关掉，下一次打开时已过期则立即执行。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p3b-"));
		const dbPath = join(dir, "s.sqlite");
		const logPath = join(dir, "sleep.log");

		const firstClock = fakeClock(T0);
		const first = await openHarness({
			dbPath,
			registry: registryWithSleep(),
			now: firstClock.now,
			setTimer: firstClock.setTimer,
		});
		const firstRoot = await first.harness.root(BACKGROUND_CONTEXT, { agent: agentOf(first.faux) });
		await first.harness.commit(
			(tx) =>
				tx.createTask(SleepTask, { at: T0 + SLEEP_FOR, logPath }, {
					ownership: { kind: "conversation" },
					conversationId: firstRoot.id,
				}),
			BACKGROUND_CONTEXT,
		);
		first.harness.resume();
		await waitFor(async () => (await kinds(first.harness)).includes("sleeping"));
		await first.close();
		const beforeRestart = await lines(logPath);

		// 新进程用"已经过去很久"的时钟打开：睡眠应立刻到期。
		const secondClock = fakeClock(T0 + SLEEP_FOR * 10);
		const second = await openHarness({
			dbPath,
			registry: registryWithSleep(),
			now: secondClock.now,
			setTimer: secondClock.setTimer,
		});
		second.harness.resume();
		const deliveredAfterRestart = await waitFor(async () => (await lines(logPath)) === 1);
		results.push({
			id: "P3-12",
			claim: "睡着的任务跨重启存活，到期后立即执行",
			observed: `重启前 deliver 次数=${beforeRestart}，重启后=${await lines(logPath)}`,
			ok: beforeRestart === 0 && deliveredAfterRestart,
		});
		await second.close();
		await rm(dir, { recursive: true, force: true });
	}

	// P3-4：deferred commit 在 withinMs 内崩溃 → 这次修改丢失，上一次已存储的值还在。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p3c-"));
		const dbPath = join(dir, "s.sqlite");
		const { promise, resolve } = Promise.withResolvers<void>();
		const child = spawn(process.execPath, ["--experimental-strip-types", STEP], {
			env: { ...process.env, STEP: "deferred-commit", DB_PATH: dbPath },
			stdio: ["ignore", "pipe", "pipe"],
		});
		child.on("exit", () => resolve());
		child.on("error", () => resolve());
		await promise;

		const reopened = await openHarness({ dbPath });
		const root = await reopened.harness.root(BACKGROUND_CONTEXT, { agent: agentOf(reopened.faux) });
		const doc = await reopened.harness.snapshot(ProbeDoc, root.id, BACKGROUND_CONTEXT);
		results.push({
			id: "P3-4",
			claim: "deferred commit 在存储前崩溃则丢失，已存储的历史值保留",
			observed: `重启后文档=${JSON.stringify(doc)}`,
			ok: doc?.note === "stored" && doc.seq === 1,
		});
		await reopened.close();
		await rm(dir, { recursive: true, force: true });
	}

	return results;
}

if (import.meta.main) {
	const results = await runP3();
	for (const result of results) {
		console.log(`${result.ok ? "PASS" : "FAIL"}  ${result.id}  ${result.claim}\n      ${result.observed}`);
	}
	if (results.some((result) => !result.ok)) process.exit(1);
}
