/**
 * U0 的"一屏实验台"：一个进程同时持有 durable Harness 和 HTTP 服务。
 *
 * 为什么 UI 与 Harness 同进程：durable 的 watch 只反映本进程自己的提交，
 * readOnly 打开另一个进程拿不到跨进程变更通知。想跨进程实时只能走 chord 的 link，
 * 那是产品路线；本实验要学的是 durable 自己的视图语义，所以宁可让"杀进程 = 页面掉线"。
 *
 * 崩溃由上游 supervise.ts 兜底重启，页面靠 EventSource 自动重连。
 */
import { mkdir, readFile } from "node:fs/promises";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";
import { dirname, join, normalize, extname } from "node:path";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import { ConversationTranscript, type ConversationView, type TaskGraph } from "@earendil-works/pi-durable";
import { agentOf, installAnswerScript, openHarness } from "../harness.ts";
import { snapshotOf, type Snapshot } from "./view.ts";

const PORT = Number(process.env.PORT ?? 7788);
const DB_PATH = process.env.DB_PATH ?? join(process.cwd(), "out", "session.sqlite");
const FAUX_TPS = Number(process.env.FAUX_TPS ?? 12);
const PUBLIC_DIR = join(process.cwd(), "public");
const VENDOR_ROOTS = ["@material", "lit", "lit-element", "lit-html", "@lit", "@lit-labs", "tslib"];

const started = Date.now();
await mkdir(dirname(DB_PATH), { recursive: true });

const { harness, faux, close } = await openHarness({ dbPath: DB_PATH, tokensPerSecond: FAUX_TPS });
installAnswerScript(faux);
const root = await harness.root(BACKGROUND_CONTEXT, { agent: agentOf(faux) });

/** 客户端的 transcript 语义由 durable 自己维护：窗口 → items 与 gap 都是它算的。 */
const transcript = new ConversationTranscript((query, limit, context) =>
	root.entries(query, limit, undefined, context),
);

const clients = new Set<ServerResponse>();
let opsCount = 0;
let latest: Snapshot | undefined;

/** 读一次当前状态并广播。所有字段都取自 watch 的 view 与 harness.usage()。 */
async function publish(view: ConversationView, graph: TaskGraph | undefined) {
	const merged = transcript.merge(view);
	const usage = (await harness.usage(BACKGROUND_CONTEXT)) as unknown as Record<string, unknown>;
	latest = snapshotOf({
		view,
		transcript: merged,
		graph,
		usage: usage as never,
		extra: {
			pid: process.pid,
			at: Date.now(),
			uptimeMs: Date.now() - started,
			ops: opsCount,
			faux: { callCount: faux.state.callCount, pending: faux.getPendingResponseCount() },
		},
	});
	broadcast();
}

function broadcast(): void {
	if (latest === undefined) return;
	const frame = `data: ${JSON.stringify(latest)}\n\n`;
	for (const client of clients) client.write(frame);
}

const watch = await root.watch(BACKGROUND_CONTEXT);
let graphValue = (await harness.watchTaskGraph(BACKGROUND_CONTEXT)).value;
let viewValue = watch.value;

watch.start(async (call) => {
	if (call.kind !== "value") return;
	opsCount += call.ops.length;
	viewValue = call.value;
	await publish(viewValue, graphValue);
});

const graphWatch = await harness.watchTaskGraph(BACKGROUND_CONTEXT);
graphWatch.start(async (call) => {
	if (call.kind !== "value") return;
	graphValue = call.value;
	await publish(viewValue, graphValue);
});

await publish(viewValue, graphValue);
// 进程被杀后重启时，这一步才真正开始"续跑上次留下的工作"。
harness.resume();

async function body(request: IncomingMessage): Promise<string> {
	const chunks: Buffer[] = [];
	for await (const chunk of request) chunks.push(chunk as Buffer);
	return Buffer.concat(chunks).toString("utf8");
}

const CONTENT_TYPES: Record<string, string> = {
	".html": "text/html; charset=utf-8",
	".js": "text/javascript; charset=utf-8",
	".mjs": "text/javascript; charset=utf-8",
	".css": "text/css; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".svg": "image/svg+xml",
};

function send(response: ServerResponse, status: number, type: string, payload: string | Buffer): void {
	response.writeHead(status, { "content-type": type, "cache-control": "no-store" });
	response.end(payload);
}

async function serveFile(response: ServerResponse, path: string): Promise<boolean> {
	try {
		const content = await readFile(path);
		send(response, 200, CONTENT_TYPES[extname(path)] ?? "application/octet-stream", content);
		return true;
	} catch {
		return false;
	}
}

const server = createServer(async (request, response) => {
	const url = new URL(request.url ?? "/", `http://localhost:${PORT}`);

	if (url.pathname === "/api/events") {
		response.writeHead(200, {
			"content-type": "text/event-stream",
			"cache-control": "no-store",
			connection: "keep-alive",
		});
		clients.add(response);
		if (latest !== undefined) response.write(`data: ${JSON.stringify(latest)}\n\n`);
		request.on("close", () => clients.delete(response));
		return;
	}

	if (url.pathname === "/api/state") {
		send(response, 200, CONTENT_TYPES[".json"]!, JSON.stringify(latest ?? null));
		return;
	}

	if (url.pathname === "/api/submit" && request.method === "POST") {
		const payload = JSON.parse((await body(request)) || "{}") as { text?: string; long?: boolean };
		// 长/短只由输入文本决定，模型侧脚本是无状态的（见 installAnswerScript）。
		const text = payload.long ? "讲一段长回答，我会在流中间杀进程。" : payload.text?.trim() || "记录一下：周五要去医院。";
		const submission = await root.submit({ type: "input", content: text }, BACKGROUND_CONTEXT);
		send(response, 200, CONTENT_TYPES[".json"]!, JSON.stringify({ id: submission.id }));
		return;
	}

	if (url.pathname === "/api/compact" && request.method === "POST") {
		const taskId = await root.compact(undefined, BACKGROUND_CONTEXT);
		send(response, 200, CONTENT_TYPES[".json"]!, JSON.stringify({ taskId }));
		return;
	}

	if (url.pathname === "/api/kill" && request.method === "POST") {
		response.end(JSON.stringify({ dying: true, pid: process.pid }));
		// 真 SIGKILL：进程内抛异常走的是"你的代码失败只影响自己"那条规则，学不到崩溃语义。
		setImmediate(() => process.kill(process.pid, "SIGKILL"));
		return;
	}

	if (url.pathname === "/api/restart" && request.method === "POST") {
		response.end(JSON.stringify({ closing: true }));
		setImmediate(() => {
			void close().finally(() => process.exit(0));
		});
		return;
	}

	if (url.pathname.startsWith("/vendor/")) {
		const rest = normalize(url.pathname.slice("/vendor/".length));
		const rootPackage = rest.split("/")[0]!;
		if (!VENDOR_ROOTS.includes(rootPackage) || rest.includes("..")) {
			send(response, 403, "text/plain; charset=utf-8", "forbidden");
			return;
		}
		if (await serveFile(response, join(process.cwd(), "node_modules", rest))) return;
		send(response, 404, "text/plain; charset=utf-8", "not found");
		return;
	}

	const file = url.pathname === "/" ? "index.html" : url.pathname.slice(1);
	if (await serveFile(response, join(PUBLIC_DIR, normalize(file)))) return;
	send(response, 404, "text/plain; charset=utf-8", "not found");
});

server.listen(PORT, () => {
	console.log(`[ui] http://localhost:${PORT}  db=${DB_PATH}  pid=${process.pid}`);
});
