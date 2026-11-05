/**
 * P4：inbox 落位顺序、watch 窗口 splice、abort 自下而上。
 *
 * 这三条都是"派生视图/调度顺序"的语义：没有崩溃，全部在同进程里直接观察，
 * 因此比 P1–P3 便宜，但它们决定 UI 与取消逻辑是否可信。
 *
 * 运行：node --experimental-strip-types src/probes/p4.ts
 */
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { fauxAssistantMessage, fauxToolCall } from "@earendil-works/pi-ai";
import { ConversationBusy } from "@earendil-works/pi-durable";
import { agentOf, installAnswerScript, openHarness } from "../harness.ts";
import { ParentTask, registryWithSlowAndTasks } from "./fixtures.ts";

export interface ProbeResult {
	readonly id: string;
	readonly claim: string;
	readonly observed: string;
	readonly ok: boolean;
}

async function lines(path: string): Promise<string[]> {
	try {
		return (await readFile(path, "utf8")).split("\n").filter((line) => line.length > 0);
	} catch {
		return [];
	}
}

async function waitFor(check: () => Promise<boolean>, timeoutMs = 10_000): Promise<boolean> {
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		if (await check()) return true;
		await new Promise((resolve) => setTimeout(resolve, 20));
	}
	return false;
}

export async function runP4(): Promise<ProbeResult[]> {
	const results: ProbeResult[] = [];

	// P4-12：busy 时的 steer / followUp / write / reject。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p4a-"));
		const opened = await openHarness({ dbPath: join(dir, "s.sqlite"), registry: registryWithSlowAndTasks() });
		const { harness, faux, close } = opened;
		const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(faux) });

		// 第一轮让模型调用慢工具，制造一个稳定的 busy 窗口（800ms，足够提交四次）。
		faux.setResponses([
			fauxAssistantMessage([fauxToolCall("probe_slow", {})], { stopReason: "toolUse" }),
			fauxAssistantMessage("第一轮结束"),
			fauxAssistantMessage("steer 之后"),
		]);
		const watch = await root.watch(BACKGROUND_CONTEXT);
		// watch.value 只在回调推进时更新；没有 start，读到的永远是最初的空视图。
		let latest = watch.value;
		watch.start((call) => {
			if (call.kind === "value") latest = call.value;
		});
		const first = await root.submit({ type: "input", content: "跑一次慢工具" }, BACKGROUND_CONTEXT);
		const becameBusy = await waitFor(async () => latest.docs["pi.live"]?.run !== undefined, 5000);

		const followUp = await root.submit({ type: "input", content: "这是个 followUp" }, BACKGROUND_CONTEXT);
		const steer = await root.submit(
			{ type: "input", content: "这是个 steer", whenBusy: "steer" },
			BACKGROUND_CONTEXT,
		);
		await root.submit({ type: "write", entry: { kind: "app.note", data: { note: "立刻可见" } } }, BACKGROUND_CONTEXT);

		let busyError: string | undefined;
		try {
			await root.submit({ type: "input", content: "busy 时应拒绝", whenBusy: "reject" }, BACKGROUND_CONTEXT);
		} catch (error) {
			busyError = error instanceof ConversationBusy ? "ConversationBusy" : String(error);
		}

		const inbox = (latest.docs["pi.inbox"] as { items?: { mode: string }[] } | undefined)?.items ?? [];
		const modesNow = inbox.map((item) => item.mode).join(",");
		const writeVisible = latest.entries.some((entry) => entry.kind === "app.note");

		await first.wait(BACKGROUND_CONTEXT);
		await harness.waitForIdle(BACKGROUND_CONTEXT);
		const page = await root.entries({ order: "ascending" }, 50, undefined, BACKGROUND_CONTEXT);
		const kinds = page.items.map((entry) => entry.kind).join(",");

		results.push({
			id: "P4-12",
			claim: "busy 时 followUp/steer 入队、write 立刻追加、reject 抛 ConversationBusy",
			observed: `busy=${becameBusy}；inbox=${modesNow || "空"}；write 立即可见=${writeVisible}；reject=${busyError ?? "未抛出"}；最终=${kinds}`,
			ok:
				becameBusy &&
				modesNow === "followUp,steer" &&
				writeVisible &&
				busyError === "ConversationBusy" &&
				followUp.id !== steer.id,
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// P4-13：watch 窗口默认 min 50 / max 100，超过 max 时一次性 splice 回 min。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p4b-"));
		const opened = await openHarness({ dbPath: join(dir, "s.sqlite") });
		const { harness, close } = opened;
		const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(opened.faux) });

		const sizes: number[] = [];
		const watch = await root.watch(BACKGROUND_CONTEXT);
		watch.start((call) => {
			if (call.kind === "value") sizes.push(call.value.entries.length);
		});
		sizes.push(watch.value.entries.length);

		for (let index = 0; index < 120; index += 1) {
			await root.commit(
				(tx) => tx.appendEntry(root.id, { kind: "app.note", data: { index } }),
				BACKGROUND_CONTEXT,
			);
		}
		await waitFor(async () => sizes.length >= 120, 10_000);
		const finalValue = watch.value;

		results.push({
			id: "P4-13",
			claim: "watch 窗口按 min=50/max=100 splice，窗口外留下 before",
			observed: `窗口大小首尾=${sizes[0]}…${sizes.at(-1)}，峰值=${Math.max(...sizes)}，出现过 50=${sizes.includes(50)}，before=${finalValue.before ?? "无"}`,
			// 120 条：涨到 100 → 超 max 时一次性 splice 回 50（丢掉 50 条）→ 再涨 19 条 = 69。
			ok:
				Math.max(...sizes) === 100 &&
				sizes.includes(50) &&
				sizes.at(-1) === 69 &&
				finalValue.before !== undefined,
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	// P4-14：abort 父任务先撤子任务，子任务的 abort handler 先跑。
	{
		const dir = await mkdtemp(join(tmpdir(), "pi-p4c-"));
		const logPath = join(dir, "abort.log");
		const opened = await openHarness({ dbPath: join(dir, "s.sqlite"), registry: registryWithSlowAndTasks() });
		const { harness, close } = opened;
		const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(opened.faux) });

		const parentId = await harness.commit(
			(tx) =>
				tx.createTask(ParentTask, { logPath }, { ownership: { kind: "conversation" }, conversationId: root.id }),
			BACKGROUND_CONTEXT,
		);
		harness.resume();
		await waitFor(async () => {
			const inspection = await harness.inspect(BACKGROUND_CONTEXT);
			return inspection.tasks.length >= 2;
		});
		await harness.abortTask(parentId, BACKGROUND_CONTEXT);
		await waitFor(async () => (await lines(logPath)).length >= 2, 10_000);
		const order = await lines(logPath);

		results.push({
			id: "P4-14",
			claim: "abort 自下而上：子任务的 abort handler 先于父任务",
			observed: `abort 顺序=${order.map((line) => line.trim()).join(" → ") || "空"}`,
			ok: order.length === 2 && order[0]!.startsWith("child") && order[1]!.startsWith("parent"),
		});
		await close();
		await rm(dir, { recursive: true, force: true });
	}

	return results;
}

if (import.meta.main) {
	const results = await runP4();
	for (const result of results) {
		console.log(`${result.ok ? "PASS" : "FAIL"}  ${result.id}  ${result.claim}\n      ${result.observed}`);
	}
	if (results.some((result) => !result.ok)) process.exit(1);
}
