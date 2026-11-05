/**
 * P2 的回归：replay 策略与 restartLimit 全部靠真崩溃验证，
 * 断言在 probe 内部完成，这里只保证四条都仍然成立。
 */
import assert from "node:assert/strict";
import { test } from "node:test";
import { runP2 } from "../src/probes/p2.ts";

test("P2：工具重跑策略 / restartLimit / 重启后任务状态", async () => {
	const results = await runP2();
	for (const result of results) {
		assert.ok(result.ok, `${result.id} 未通过：${result.observed}`);
	}
	assert.deepEqual(
		results.map((result) => result.id),
		["P2-1", "P2-9", "P2-2", "P2-3"],
	);
});
