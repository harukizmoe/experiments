/**
 * P3 的夹具：一个会睡到指定时刻的任务、一个 latest/initial 的会话文档、以及一个可推进的假时钟。
 *
 * 假时钟必须同时替换 `now` 与 `setTimer`：只改 now，调度器仍会用真实 setTimeout 唤醒，
 * 时间就推进不动；只改 setTimer 而不改 now，睡着的任务算出来的截止时刻仍是真实时间。
 */
import { appendFile } from "node:fs/promises";
import { setTimeout as delay } from "node:timers/promises";
import type { SetTimer, TaskId } from "@earendil-works/pi-durable";
import { createRegistry, defineDoc, defineExtension, defineTask, defineTool } from "@earendil-works/pi-durable";
import { obj } from "@earendil-works/pi-schema";

export interface SleepInput {
	readonly at: number;
	readonly logPath: string;
}

type SleepState = { readonly phase: "wait" } | { readonly phase: "deliver" };

/** 两个 phase：wait 把睡眠写进 checkpoint，deliver 才产生可观察副作用。 */
export const SleepTask = defineTask<SleepInput, SleepState, null>({
	name: "probe.sleep",
	version: 1,
	initial: () => ({ phase: "wait" }),
	phases: {
		wait: (task, runtime, context) => runtime.sleepUntil(task.input.at, { phase: "deliver" }, context),
		deliver: async (task, runtime, context) => {
			await appendFile(task.input.logPath, `delivered ${task.input.at}\n`);
			await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), context);
		},
	},
	abort: (_task, runtime, context) =>
		runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), context),
});

/** latest + fork:"initial"：只有这种文档的提交允许 deferred 存储（#4 用它观察丢失窗口）。 */
export const ProbeDoc = defineDoc<{ readonly note: string; readonly seq: number }>({
	kind: "probe.deferred",
	version: 1,
	scope: "conversation",
	history: "latest",
	fork: "initial",
	initial: () => ({ note: "init", seq: 0 }),
	checkpointWhen: () => false,
});

export function registryWithSleep() {
	const registry = createRegistry();
	registry.install(defineExtension({ name: "probe-sleep", tasks: [SleepTask] }));
	return registry;
}

export interface FakeClock {
	now(): number;
	setTimer: SetTimer;
	/** 推进到指定时刻并触发所有到期回调。 */
	advanceTo(time: number): void;
	readonly pending: { at: number; callback: () => void }[];
}

/** 最小的可控时钟：记录所有 timer，只有显式 advance 时才触发。 */
export function fakeClock(start: number): FakeClock {
	let current = start;
	const pending: { at: number; callback: () => void }[] = [];

	const setTimer: SetTimer = (callback, delayMs) => {
		const entry = { at: current + delayMs, callback };
		pending.push(entry);
		return () => {
			const index = pending.indexOf(entry);
			if (index >= 0) pending.splice(index, 1);
		};
	};

	return {
		now: () => current,
		setTimer,
		pending,
		advanceTo(time) {
			current = time;
			// 一次性把到期项取出来执行：回调里可能再 arm 新 timer，新 timer 不应在本轮被提前触发。
			const due = pending.filter((entry) => entry.at <= current);
			for (const entry of due) {
				const index = pending.indexOf(entry);
				if (index >= 0) pending.splice(index, 1);
				entry.callback();
			}
		},
	};
}


/** 慢工具：让 run 稳定地 busy 一段时间，好在中间提交 steer / followUp / write。 */
export const SlowTool = defineTool({
	name: "probe_slow",
	description: "等 800ms 再返回，用于制造 busy 窗口",
	parameters: obj({}),
	execute: async () => {
		await delay(800);
		return { output: [{ type: "text", text: "slow done" }] };
	},
});

type AbortLog = { readonly logPath: string };

/** 子任务：一直跑到被 abort；abort 时先写日志再终结。 */
const ChildTask = defineTask<AbortLog, { phase: "work" }, null>({
	name: "probe.child",
	version: 1,
	initial: () => ({ phase: "work" }),
	phases: {
		work: async (_task, runtime, context) => {
			// 用 sleepUntil 而不是忙等：睡着的任务没有 invocation，abort mark 不必等它返回。
			await runtime.sleepUntil(Date.now() + 30_000, { phase: "work" }, context);
		},
	},
	abort: async (task, runtime, context) => {
		await appendFile(task.input.logPath, "child abort\n");
		await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), context);
	},
});

type ParentState = { readonly phase: "spawn" } | { readonly phase: "decide"; readonly child: TaskId };

/** 父任务：建子任务并 waiting 等它；abort 时写日志再终结（文档说子任务的 abort 先跑）。 */
const ParentTask = defineTask<AbortLog, ParentState, null>({
	name: "probe.parent",
	version: 1,
	initial: () => ({ phase: "spawn" }),
	phases: {
		spawn: async (task, runtime, context) => {
			await runtime.commit(async (tx) => {
				const child = await tx.createTask(
					ChildTask,
					{ logPath: task.input.logPath },
					{ ownership: { kind: "task", taskId: task.id } },
				);
				return { status: "waiting", checkpoint: { phase: "decide", child }, on: [child], policy: "failFast" };
			}, context);
		},
		decide: async (_task, runtime, context) => {
			await runtime.commit(() => ({ status: "terminal", outcome: { status: "completed", result: null } }), context);
		},
	},
	abort: async (task, runtime, context) => {
		await appendFile(task.input.logPath, "parent abort\n");
		await runtime.commit(() => ({ status: "terminal", outcome: { status: "aborted" } }), context);
	},
});

export function registryWithSlowAndTasks() {
	const registry = createRegistry();
	registry.install(defineExtension({ name: "probe-slow", tools: [SlowTool] }));
	registry.install(defineExtension({ name: "probe-tasks", tasks: [ChildTask, ParentTask] }));
	return registry;
}

export { ChildTask, ParentTask };
