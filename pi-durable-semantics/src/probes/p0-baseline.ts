/**
 * P0：faux 模型穿过 Harness 的接线验证 + 一次完整提交的可见状态。
 *
 * 这是所有后续 probe 的地基：如果 faux 不能在 Harness 里产生一次稳定回答，
 * 后面所有关于崩溃/重放的断言都没有可信的对照组。
 *
 * 运行：node --experimental-strip-types src/probes/p0-baseline.ts
 */
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { agentOf, openHarness, queueTextAnswer } from "../harness.ts";

const dir = await mkdtemp(join(tmpdir(), "pi-durable-p0-"));
const dbPath = join(dir, "session.sqlite");

const { harness, faux, close } = await openHarness({ dbPath });
let failed = false;

try {
	queueTextAnswer(faux, "巴黎是法国首都。");

	const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(faux) });
	const submission = await root.submit({ type: "input", content: "法国的首都是哪里？" }, BACKGROUND_CONTEXT);
	const settled = await submission.wait(BACKGROUND_CONTEXT);

	const entries = await root.entries({ order: "ascending" }, 100, undefined, BACKGROUND_CONTEXT);
	const kinds = entries.items.map((entry) => entry.kind);
	const usage = await harness.usage(BACKGROUND_CONTEXT);

	console.log("submission.status      ", settled.status);
	console.log("entries.kinds          ", kinds.join(" -> "));
	console.log("entries.count          ", entries.items.length);
	console.log("usage.models           ", JSON.stringify(usage.models));
	console.log("faux.callCount         ", faux.state.callCount);

	// 断言 1：一次输入必须走到 done。
	failed ||= settled.status !== "done";
	// 断言 2：transcript 的第一条是用户输入，且只有一轮模型回答。
	failed ||= kinds[0] !== "pi.user";
	failed ||= kinds.filter((kind) => kind === "pi.assistant").length !== 1;
	// 断言 3：faux 恰好被调用一次——多于一次说明有额外的重试/压缩请求混进来。
	failed ||= faux.state.callCount !== 1;
	// 断言 4：usage 里记录了这次模型调用，否则计费口径不可信。
	failed ||= Object.keys(usage.models).length === 0;
} finally {
	await close();
	await rm(dir, { recursive: true, force: true });
}

if (failed) {
	console.error("P0 FAILED");
	process.exit(1);
}
console.log("P0 OK");
