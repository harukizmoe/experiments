/**
 * P4 的回归：inbox 顺序、watch 窗口 splice、abort 级联。
 * 断言在 probe 内部完成，这里只保证三条仍然成立。
 */
import assert from "node:assert/strict";
import { test } from "node:test";
import { runP4 } from "../src/probes/p4.ts";

test("P4：inbox 落位顺序 / watch 窗口 splice / abort 自下而上", async () => {
	const results = await runP4();
	for (const result of results) {
		assert.ok(result.ok, `${result.id} 未通过：${result.observed}`);
	}
	assert.deepEqual(
		results.map((result) => result.id),
		["P4-12", "P4-13", "P4-14"],
	);
});
