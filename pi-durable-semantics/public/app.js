/**
 * 实验台的浏览器侧：只做三件事——连 SSE、渲染快照、把控制按钮打到服务端。
 *
 * 这里刻意不实现任何 durable 逻辑：断档判断也只看 entry id 的先后
 * （entry id 是 UUIDv7，字符串比较即时间序），因为真正的 gap/fillGap 由
 * 服务端的 ConversationTranscript 负责。
 */
import "@material/web/button/filled-button.js";
import "@material/web/button/filled-tonal-button.js";
import "@material/web/button/outlined-button.js";
import "@material/web/tabs/primary-tab.js";
import "@material/web/tabs/tabs.js";

const dot = document.getElementById("dot");
const meta = document.getElementById("meta");
const badges = document.getElementById("badges");
const entries = document.getElementById("entries");
const count = document.getElementById("count");
const panel = document.getElementById("panel");
const events = document.getElementById("events");
const tabs = document.getElementById("tabs");

let lastPid;
let lastNewest;
let snapshot;
let activeTab = 0;
/**
 * 粘性徽标：RESTARTED / RESYNC 描述的是"这一页经历过一次断档"，只发生一帧。
 * 如果每帧重算，它们会在下一次提交到来时立刻消失，读者根本看不到——
 * 所以一旦置位就保留到页面刷新。
 */
const stickyBadges = new Set();

const PANELS = [
	(view) => view.live,
	(view) => view.inbox,
	(view) => view.graph,
	(view) => view.usage,
];

tabs.addEventListener("change", () => {
	activeTab = tabs.activeTabIndex ?? 0;
	renderPanel();
});

function badge(text, tone = "") {
	const node = document.createElement("span");
	node.className = `badge ${tone}`.trim();
	node.textContent = text;
	badges.append(node);
}

function row(text, kind, extraClass = "") {
	const node = document.createElement("div");
	node.className = `row ${extraClass}`.trim();
	if (kind) {
		const tag = document.createElement("span");
		tag.className = "kind";
		tag.textContent = kind;
		node.append(tag);
	}
	const body = document.createElement("span");
	body.className = "text";
	body.textContent = text;
	node.append(body);
	return node;
}

function renderTranscript(view) {
	entries.replaceChildren();
	for (const item of view.transcript.rows) {
		if (item.kind === "entry") {
			entries.append(row(item.text || "（无文本）", item.entryKind + (item.byTaskId ? " ·task" : "")));
		} else if (item.kind === "gap") {
			entries.append(row(`断档：${item.id} 之间缺少 entry`, "gap", "gap"));
		} else {
			entries.append(row("更早的内容在窗口之外（窗口外不出现在本次推送）", "earlier"));
		}
	}
	count.textContent = `${view.transcript.rows.length} 行`;
}

function renderPanel() {
	const pick = PANELS[activeTab];
	panel.textContent = JSON.stringify(pick(snapshot) ?? null, null, 2);
}

function render(view) {
	snapshot = view;

	// 断档/重启判定：pid 变了说明承载 Harness 的进程死过一次；
	// newest 回退说明客户端见过的 entry 在新进程的窗口里不存在了——真正的数据缺口。
	const restarted = lastPid !== undefined && view.pid !== lastPid;
	const resynced = lastNewest !== undefined && view.transcript.newest !== undefined && view.transcript.newest < lastNewest;
	lastPid = view.pid;
	if (view.transcript.newest !== undefined) lastNewest = view.transcript.newest;

	if (restarted) stickyBadges.add("RESTARTED");
	if (resynced) stickyBadges.add("RESYNC");

	badges.replaceChildren();
	for (const text of stickyBadges) badge(text, "warn");
	// runFailure 是对象（{reason, detail, inputs...}），显示 reason；完整内容在 Live 面板里。
	const runFailure = view.live?.runFailure;
	if (runFailure) badge(`runFailure: ${runFailure.reason}`, "error");
	const retry = view.live?.generation?.retry;
	if (retry) badge(`retry @${new Date(retry.at).toLocaleTimeString()}`, "warn");
	if (view.live?.generation?.attempt > 1) badge(`attempt ${view.live.generation.attempt}`);
	if (view.live?.run) badge("RUNNING");
	if (view.faux.pending > 0) badge(`faux pending ${view.faux.pending}`);

	dot.className = "dot live";
	meta.textContent = `pid ${view.pid} · up ${(view.uptimeMs / 1000).toFixed(0)}s · conv ${view.conversationId}`;
	renderTranscript(view);
	renderPanel();
	events.textContent = `事件流：本进程 ops ${view.ops} · 本进程 faux 调用 ${view.faux.callCount} · 最近快照 ${new Date(view.at).toLocaleTimeString()}`;
}

const source = new EventSource("/api/events");
source.onmessage = (message) => render(JSON.parse(message.data));
source.onerror = () => {
	// EventSource 自动重连；这里只把灯打红，重连成功后第一帧会把状态改回来。
	dot.className = "dot dead";
	meta.textContent = "连接中断，等待重连…";
};

async function post(path, payload) {
	const response = await fetch(path, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify(payload ?? {}),
	});
	return response.json().catch(() => ({}));
}

document.getElementById("submit-short").addEventListener("click", () => {
	void post("/api/submit", { text: `记住：${new Date().toLocaleTimeString()} 的一次输入` });
});
document.getElementById("submit-long").addEventListener("click", () => {
	void post("/api/submit", { text: "讲一段长回答，我会在流中间杀进程。", long: true });
});
document.getElementById("compact").addEventListener("click", () => {
	void post("/api/compact");
});
document.getElementById("restart").addEventListener("click", () => {
	void post("/api/restart");
});
document.getElementById("kill").addEventListener("click", () => {
	void post("/api/kill");
});
