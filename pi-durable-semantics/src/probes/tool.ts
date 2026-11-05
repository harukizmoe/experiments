/**
 * P2 的夹具：一个会写副作用、并且可以按环境变量"在执行中自杀"的工具。
 *
 * 副作用写成追加文件而不是计数器：进程反复重启后，唯一可信的证据是磁盘上的行数。
 * 崩溃行为由环境变量控制，因为工具代码本身就是被注入崩溃的地方——
 * 用工具内部的自杀代替父进程定时 kill，才能保证崩溃点精确落在 execute() 中间。
 */
import { appendFile } from "node:fs/promises";
import { obj, str } from "@earendil-works/pi-schema";
import { createRegistry, defineExtension, defineTool } from "@earendil-works/pi-durable";

export interface SideEffectToolOptions {
	/** 副作用日志路径；每次执行追加一行。 */
	readonly logPath: string;
	/** `safe` 时崩溃后允许重跑；默认 `unsafe`（模型会收到 interrupted 错误结果）。 */
	readonly replay?: "safe" | "unsafe";
	/** 每次执行都自杀（用于 restartLimit probe）。 */
	readonly killEveryRun: boolean;
}

/** 装好夹具工具并返回注册表；重开存储时必须用同一个定义，否则任务会 blocked。 */
export function registryWithSideEffect(options: SideEffectToolOptions) {
	const tool = defineTool({
		name: "probe_side_effect",
		description: "追加一行到副作用日志；实验用它观察崩溃后是否重跑",
		parameters: obj({ note: str() }),
		replay: options.replay ?? "unsafe",
		execute: async (args) => {
			await appendFile(options.logPath, `run ${Date.now()} ${args.note}\n`);
			if (options.killEveryRun) process.kill(process.pid, "SIGKILL");
			return { output: [{ type: "text", text: `已记录：${args.note}` }] };
		},
	});

	const registry = createRegistry();
	registry.install(defineExtension({ name: "probe-side-effect", tools: [tool] }));
	return { registry, tool };
}
