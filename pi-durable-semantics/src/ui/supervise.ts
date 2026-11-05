/**
 * 进程监护：收到 SIGKILL 就重启子进程。
 *
 * 为什么要单独一个进程：崩溃实验必须能让"承载 Harness 的进程"真的消失——进程内 throw
 * 走的是"你的代码失败只影响自己的单元"那条规则，根本不会触发 durable 的崩溃计数与恢复路径。
 * 监护进程自己不做任何 durable 的事，只负责让页面还能重连。
 */
import { spawn } from "node:child_process";

let stopping = false;
let child: ReturnType<typeof spawn> | undefined;
let restarts = 0;

function launch(): void {
	child = spawn(process.execPath, ["--experimental-strip-types", "src/ui/server.ts"], {
		stdio: "inherit",
		env: process.env,
	});
	child.on("exit", (code, signal) => {
		if (stopping) return;
		restarts += 1;
		console.log(`[supervise] 子进程退出（${signal ?? code}），第 ${restarts} 次重启`);
		setTimeout(launch, 300);
	});
}

process.on("SIGINT", () => {
	stopping = true;
	child?.kill("SIGINT");
	process.exit(0);
});

launch();
