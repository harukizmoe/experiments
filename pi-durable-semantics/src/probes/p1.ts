/**
 * P1：提交边界、commit 原子性、requestId 幂等。
 *
 * 三个 probe 都需要"进程真的死掉再读存储"，所以结构统一是：
 * 子进程走到语义位置 → SIGKILL → 父进程重新打开同一个 SQLite → 断言存储里的事实。
 *
 * 运行：node --experimental-strip-types src/probes/p1.ts
 */
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { agentOf, installAnswerScript, openHarness } from "../harness.ts";

export interface ProbeResult {
	readonly id: string;
	readonly claim: string;
	readonly observed: string;
	readonly ok: boolean;
}

/** 跑一段会把自身 SIGKILL 的子进程；返回退出信号，非 SIGKILL 视为夹具故障。 */
function crashChild(step: string, dbPath: string): Promise<string> {
	const { promise, resolve, reject } = Promise.withResolvers<string>();
	const child = spawn(
		process.execPath,
		["--experimental-strip-types", join(import.meta.dirname, "..", "crash", "step.ts")],
		{ env: { ...process.env, STEP: step, DB_PATH: dbPath }, stdio: ["ignore", "pipe", "pipe"] },
	);
	let stderr = "";
	child.stderr.on("data", (chunk) => (stderr += String(chunk)));
	child.on("error", (error) => reject(error));
	child.on("exit", (code, signal) => {
		if (signal !== "SIGKILL") reject(new Error(`期望 SIGKILL，实际 ${signal ?? code}：${stderr}`));
		else resolve(signal);
	});
	return promise;
}

/** 重新打开同一个库；调用方负责 close。 */
async function reopen(dbPath: string) {
	const opened = await openHarness({ dbPath });
	installAnswerScript(opened.faux);
	const root = await opened.harness.root(BACKGROUND_CONTEXT, { agent: agentOf(opened.faux) });
	return { ...opened, root };
}

export async function runP1(): Promise<ProbeResult[]> {
	const results: ProbeResult[] = [];

	// #1 左半边：submit 之前死，transcript 必须为空。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p1a-"));
		const dbPath = join(dir, "s.sqlite");
		await crashChild("before-submit", dbPath);
		const { close, root } = await reopen(dbPath);
		const page = await root.entries({ order: "ascending" }, 10, undefined, BACKGROUND_CONTEXT);
		results.push({
			id: "P1-1a",
			claim: "submit 之前崩溃，输入不存在",
			observed: `entries=${page.items.length}`,
			ok: page.items.length === 0,
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// #1 右半边：submit() resolve 之后立刻死，输入已在存储里，且 resume 会把 run 跑完。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p1b-"));
		const dbPath = join(dir, "s.sqlite");
		await crashChild("after-submit", dbPath);
		const { harness, close, root } = await reopen(dbPath);
		const before = await root.entries({ order: "ascending" }, 10, undefined, BACKGROUND_CONTEXT);
		harness.resume();
		await harness.waitForIdle(BACKGROUND_CONTEXT);
		const after = await root.entries({ order: "ascending" }, 10, undefined, BACKGROUND_CONTEXT);
		const kindsBefore = before.items.map((entry) => entry.kind).join(",");
		const kindsAfter = after.items.map((entry) => entry.kind).join(",");
		results.push({
			id: "P1-1b",
			claim: "submit() resolve 即已落盘，resume 后 run 继续",
			observed: `崩溃后=${kindsBefore || "空"} → resume 后=${kindsAfter}`,
			ok: kindsBefore === "pi.user" && kindsAfter === "pi.user,pi.assistant",
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// #2：commit 回调抛错，整批回滚。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p1c-"));
		const dbPath = join(dir, "s.sqlite");
		await crashChild("atomic-commit", dbPath);
		const { close, root } = await reopen(dbPath);
		const page = await root.entries({ order: "ascending" }, 10, undefined, BACKGROUND_CONTEXT);
		const leftovers = page.items.map((entry) => entry.kind);
		results.push({
			id: "P1-2",
			claim: "commit 回调抛错则整批回滚",
			observed: `残留 entries=${leftovers.length === 0 ? "无" : leftovers.join(",")}`,
			ok: leftovers.length === 0,
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// #3：跨进程重发同一个 requestId，只能有一个 submission 与一条输入。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p1d-"));
		const dbPath = join(dir, "s.sqlite");
		await crashChild("request-id", dbPath);
		const { close, root } = await reopen(dbPath);
		const second = await root.submit(
			{ type: "input", content: "幂等输入", requestId: "probe-p1-request" },
			BACKGROUND_CONTEXT,
		);
		const page = await root.entries({ order: "ascending" }, 10, undefined, BACKGROUND_CONTEXT);
		const userEntries = page.items.filter((entry) => entry.kind === "pi.user").length;
		results.push({
			id: "P1-3",
			claim: "重复 requestId 幂等（跨重启）",
			observed: `重发沿用 submission ${String(second.id)}，pi.user 条数=${userEntries}`,
			ok: userEntries === 1,
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	return results;
}

if (import.meta.main) {
	const results = await runP1();
	for (const result of results) {
		console.log(`${result.ok ? "PASS" : "FAIL"}  ${result.id}  ${result.claim}\n      ${result.observed}`);
	}
	if (results.some((result) => !result.ok)) process.exit(1);
}
