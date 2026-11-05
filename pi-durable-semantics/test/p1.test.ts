/**
 * P1 的回归：每个 probe 都真杀一个子进程再读存储，所以这里只断言结果表，
 * 细节（崩溃后 entries、resume 后是否补出回答）由 probe 自己打印。
 */
import assert from "node:assert/strict";
import { test } from "node:test";
import { runP1 } from "../src/probes/p1.ts";

test("P1：提交边界 / commit 原子性 / requestId 幂等", async () => {
	const results = await runP1();
	for (const result of results) {
		assert.ok(result.ok, `${result.id} 未通过：${result.observed}`);
	}
	assert.deepEqual(
		results.map((result) => result.id),
		["P1-1a", "P1-1b", "P1-2", "P1-3"],
	);
});
