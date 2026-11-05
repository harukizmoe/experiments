/**
 * U0 的回归：P0 语义 + 视图快照 + import map 覆盖。
 *
 * import map 这条断言来自一次真实故障：@material/web 依赖 lit，而 lit 的入口再导出
 * `lit-element/lit-element.js`，import map 漏了它，页面直接白屏且服务端 200——
 * 只有浏览器报 "does not resolve to a valid URL"。所以这里静态扫描一遍 vendor 里的裸模块名。
 */
import assert from "node:assert/strict";
import { readFile, readdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { ConversationTranscript } from "@earendil-works/pi-durable";
import { agentOf, openHarness, queueTextAnswer } from "../src/harness.ts";
import { snapshotOf, transcriptRows } from "../src/ui/view.ts";

test("P0：faux 穿过 Harness，一次输入产生 user + 单次 assistant", async () => {
	const dir = await mkdtemp(join(tmpdir(), "pi-durable-test-"));
	const { harness, faux, close } = await openHarness({ dbPath: join(dir, "s.sqlite") });
	try {
		queueTextAnswer(faux, "巴黎是法国首都。");
		const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(faux) });
		const settled = await (
			await root.submit({ type: "input", content: "法国的首都是哪里？" }, BACKGROUND_CONTEXT)
		).wait(BACKGROUND_CONTEXT);

		assert.equal(settled.status, "done");
		assert.equal(faux.state.callCount, 1, "一次输入只应触发一次模型请求");

		const page = await root.entries({ order: "ascending" }, 100, undefined, BACKGROUND_CONTEXT);
		assert.deepEqual(
			page.items.map((entry) => entry.kind),
			["pi.user", "pi.assistant"],
		);

		const usage = await harness.usage(BACKGROUND_CONTEXT);
		assert.ok(Object.keys(usage.models).length > 0, "usage 必须记录这次模型调用");
	} finally {
		await close();
		await rm(dir, { recursive: true, force: true });
	}
});

test("视图快照：窗口内的 entry 变成可渲染行，并给出 newest 供断档比较", async () => {
	const dir = await mkdtemp(join(tmpdir(), "pi-durable-view-"));
	const { harness, faux, close } = await openHarness({ dbPath: join(dir, "s.sqlite") });
	try {
		queueTextAnswer(faux, "记下了。");
		const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(faux) });
		await (
			await root.submit({ type: "input", content: "记住：周三交报告" }, BACKGROUND_CONTEXT)
		).wait(BACKGROUND_CONTEXT);

		const transcript = new ConversationTranscript((query, limit, context) =>
			root.entries(query, limit, undefined, context),
		);
		const watch = await root.watch(BACKGROUND_CONTEXT);
		const rows = transcriptRows(transcript.merge(watch.value));
		assert.deepEqual(
			rows.map((row) => row.kind),
			["entry", "entry"],
		);

		const snapshot = snapshotOf({
			view: watch.value,
			transcript: transcript.merge(watch.value),
			graph: undefined,
			usage: null,
			extra: { pid: process.pid, at: 0, uptimeMs: 0, ops: 0, faux: { callCount: 0, pending: 0 } },
		});
		assert.equal(snapshot.transcript.newest, watch.value.entries.at(-1)?.id);
		assert.match(snapshot.transcript.rows[0]!.text ?? "", /周三交报告/);
	} finally {
		await close();
		await rm(dir, { recursive: true, force: true });
	}
});

/** 从 HTML 的 import map 里读出裸模块名 → 前缀表。 */
async function importMap(): Promise<Map<string, string>> {
	const html = await readFile(join(import.meta.dirname, "..", "public", "index.html"), "utf8");
	const json = html.match(/<script type="importmap">([\s\S]*?)<\/script>/)?.[1];
	assert.ok(json, "index.html 必须包含 import map");
	const parsed = JSON.parse(json) as { imports: Record<string, string> };
	return new Map(Object.entries(parsed.imports));
}

test("import map 覆盖所有被浏览器加载的裸模块名（漏一个就是白屏）", async () => {
	const map = await importMap();
	const roots = ["@material/web", "lit", "lit-element", "lit-html", "@lit/reactive-element", "@lit/context", "@lit-labs/ssr-dom-shim", "tslib"];
	const vendor = join(import.meta.dirname, "..", "node_modules");
	const missing = new Set<string>();

	async function walk(dir: string): Promise<void> {
		for (const entry of await readdir(dir, { withFileTypes: true })) {
			const path = join(dir, entry.name);
			if (entry.isDirectory()) {
				// development/ 与 src/ 是给打包器和类型用的，浏览器走的是包根导出的 dist 入口。
				if (entry.name === "development" || entry.name === "src" || entry.name === "internal") continue;
				await walk(path);
			} else if (entry.name.endsWith(".js") || entry.name.endsWith(".mjs")) {
				const source = await readFile(path, "utf8");
				for (const match of source.matchAll(/(?:from|import)\s*["']([^"']+)["']/g)) {
					const specifier = match[1]!;
					if (specifier.startsWith(".") || specifier.startsWith("/") || specifier.startsWith("node:")) continue;
					// 样式/JSON 的裸导入走不了 import map（浏览器只对它解析 JS 模块），页面也没有实际加载它们。
					if (/\.(css|scss|json|wasm)$/.test(specifier)) continue;
					const covered = map.has(specifier) || [...map.keys()].some((key) => key.endsWith("/") && specifier.startsWith(key));
					if (!covered) missing.add(specifier);
				}
			}
		}
	}

	for (const root of roots) await walk(join(vendor, root));
	assert.deepEqual([...missing], [], `import map 缺少：${[...missing].join(", ")}`);
});
