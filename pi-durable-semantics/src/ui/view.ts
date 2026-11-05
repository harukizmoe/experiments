/**
 * 把 durable 的可见状态压缩成浏览器要的 JSON。
 *
 * 为什么不让浏览器直接读 storage：UI 一旦有自己的读取路径，它就成了第二个事实来源，
 * "页面上没显示" 会被误当成 "durable 没提交"。所有字段都来自 watch 的 ConversationView、
 * 任务图 watch 和 Harness 的 usage，浏览器只做渲染。
 */
import type {
	ConversationView,
	EntryRecord,
	JsonObject,
	TaskGraph,
	TranscriptView,
} from "@earendil-works/pi-durable";

/** 一条可渲染的 entry：只带展示需要的字段，不带完整 model 消息（体积大且与断言无关）。 */
export interface EntryRow {
	readonly id: string;
	readonly kind: string;
	readonly byTaskId?: string;
	/** 从 model 消息或 data 里提取的一句预览。 */
	readonly text: string;
}

export interface TranscriptRow {
	readonly kind: "entry" | "gap" | "earlier";
	readonly id?: string;
	readonly entryKind?: string;
	readonly text?: string;
	readonly byTaskId?: string;
}

export interface TaskRow {
	readonly id: string;
	readonly kind: string;
	readonly status: string;
	readonly owner?: string;
	readonly background: boolean;
}

export interface Snapshot {
	readonly type: "snapshot";
	readonly pid: number;
	readonly at: number;
	readonly uptimeMs: number;
	readonly conversationId: string;
	readonly transcript: {
		readonly rows: readonly TranscriptRow[];
		/** 客户端据此判断重连后是否发生了断档（entry id 是 UUIDv7，字符串比较即时间序）。 */
		readonly newest?: string;
	};
	readonly live: JsonObject | null;
	readonly inbox: JsonObject | null;
	readonly usage: JsonObject | null;
	readonly graph: readonly TaskRow[];
	readonly ops: number;
	readonly faux: { readonly callCount: number; readonly pending: number };
}

/** 从 entry 里取一句人能看的文字；model 消息优先，其次 data。 */
function preview(entry: EntryRecord): string {
	const parts: string[] = [];
	for (const message of entry.model ?? []) {
		if (typeof message.content === "string") parts.push(message.content);
		else if (Array.isArray(message.content)) {
			for (const block of message.content) {
				if (block && typeof block === "object" && "text" in block && typeof block.text === "string") {
					parts.push(block.text);
				}
			}
		}
	}
	if (parts.length === 0 && entry.data !== undefined) parts.push(JSON.stringify(entry.data));
	return parts.join(" ").replace(/\s+/g, " ").trim().slice(0, 400);
}

export function entryRow(entry: EntryRecord): EntryRow {
	return {
		id: entry.id,
		kind: entry.kind,
		...(entry.byTaskId === undefined ? {} : { byTaskId: entry.byTaskId }),
		text: preview(entry),
	};
}

/** TranscriptItem → 渲染行；gap/earlier 原样保留，客户端据此画断档与"加载更早"。 */
export function transcriptRows(view: TranscriptView): TranscriptRow[] {
	return view.items.map((item): TranscriptRow => {
		if (item.kind === "entry") {
			return {
				kind: "entry",
				id: item.entry.id,
				entryKind: item.entry.kind,
				text: preview(item.entry),
				...(item.entry.byTaskId === undefined ? {} : { byTaskId: item.entry.byTaskId }),
			};
		}
		if (item.kind === "gap") return { kind: "gap", id: `${item.after}..${item.upTo}` };
		return { kind: "earlier" };
	});
}

export function taskRows(graph: TaskGraph | undefined): TaskRow[] {
	if (graph === undefined) return [];
	return Object.values(graph.tasks).map((node) => ({
		id: node.id,
		kind: node.kind,
		status: node.state.status,
		...(node.owner === undefined ? {} : { owner: node.owner }),
		background: node.background,
	}));
}

/** built-in 文档按 kind 存在 view.docs 里；缺省就是没有这个文档（例如空闲时没有 pi.inbox）。 */
export function docOf(view: ConversationView, kind: string): JsonObject | null {
	return (view.docs[kind] as JsonObject | undefined) ?? null;
}

export function snapshotOf(args: {
	view: ConversationView;
	transcript: TranscriptView;
	graph: TaskGraph | undefined;
	usage: JsonObject | null;
	extra: { pid: number; at: number; uptimeMs: number; ops: number; faux: Snapshot["faux"] };
}): Snapshot {
	// TranscriptView 只有 items（客户端视角的整条记录），没有 entries 窗口；
	// newest 取最后一条 entry 项——它同时是"重连后是否断档"的比较基准。
	const newest = [...args.transcript.items].reverse().find((item) => item.kind === "entry")?.entry.id;
	return {
		type: "snapshot",
		pid: args.extra.pid,
		at: args.extra.at,
		uptimeMs: args.extra.uptimeMs,
		conversationId: args.view.conversation.id,
		transcript: {
			rows: transcriptRows(args.transcript),
			...(newest === undefined ? {} : { newest }),
		},
		live: docOf(args.view, "pi.live"),
		inbox: docOf(args.view, "pi.inbox"),
		usage: args.usage,
		graph: taskRows(args.graph),
		ops: args.extra.ops,
		faux: args.extra.faux,
	};
}
