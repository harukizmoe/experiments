/**
 * P3 的回归：睡眠/假时钟/跨重启唤醒/deferred 丢失窗口。
 * 断言在 probe 内部完成，这里只保证四条仍然成立。
 */
import assert from "node:assert/strict";
import { test } from "node:test";
import { runP3 } from "../src/probes/p3.ts";

test("P3：睡眠状态 / 假时钟唤醒 / 跨重启到期 / deferred 丢失窗口", async () => {
	const results = await runP3();
	for (const result of results) {
		assert.ok(result.ok, `${result.id} 未通过：${result.observed}`);
	}
	assert.deepEqual(
		results.map((result) => result.id),
		["P3-10", "P3-11", "P3-12", "P3-4"],
	);
});
