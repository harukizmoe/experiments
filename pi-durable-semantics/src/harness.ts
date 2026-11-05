/**
 * 一个 durable Harness 的最小工厂：faux 模型 + SQLite 存储。
 *
 * 为什么用 faux 而不是真实模型：本实验的断言全是"第 N 轮模型会调用工具 / 流到一半被杀掉"这类
 * 时序与副作用问题，真实模型的随机性会让 probe 变成 flaky，测出来的失败无法归因。
 * faux 的响应由脚本给定，因此同一段代码在任意机器、任意次数下行为一致。
 */
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";
import {
	createModels,
	fauxAssistantMessage,
	fauxProvider,
	fauxText,
	type FauxProviderHandle,
	type FauxResponseFactory,
} from "@earendil-works/pi-ai";
import {
	Harness,
	createRegistry,
	type HarnessSettings,
	type Registry,
	type SetTimer,
} from "@earendil-works/pi-durable";
import { openNodeSqliteStorage } from "@earendil-works/pi-durable/storage/sqlite/node";

/** 会话默认 agent 的模型取自 faux 自己的目录，避免硬编码 provider/modelId 与依赖版本脱钩。 */
export function agentOf(faux: FauxProviderHandle) {
	const model = faux.getModel();
	return { model: { provider: model.provider, modelId: model.id } };
}

export interface OpenHarnessOptions {
	/** SQLite 文件路径；`:memory:` 这类内存库不在本实验范围内，MemoryStorage 另用。 */
	dbPath: string;
	/** 要安装的扩展/工具；崩溃恢复必须用同一个注册表重开，否则任务会卡在 blocked。 */
	registry?: Registry;
	/** 运行策略（重试、restartLimit 等）。恢复实验里必须和崩溃前一致。 */
	settings?: HarnessSettings;
	/** 宿主时钟（毫秒）。睡眠截止时刻存在这个时钟上，恢复实验里必须与崩溃前一致。 */
	now?: () => number;
	/** 宿主 timer；假时钟必须同时替换它与 `now`，否则调度器仍按真实时间唤醒。 */
	setTimer?: SetTimer;
	/**
	 * faux 的流式速率（token/秒）。默认 0 表示一次性给出整段；
	 * 崩溃 probe 需要一个能"卡在流中间"的窗口，因此 UI 会把它调慢。
	 */
	tokensPerSecond?: number;
}

export interface OpenedHarness {
	harness: Harness;
	faux: FauxProviderHandle;
	close: () => Promise<void>;
}

/**
 * 打开 Harness。storage 的所有权规则：同一文件同一时间只允许一个 Harness 写，
 * 因此这里不做任何重试或加锁——重复打开是调用方的错误，应当直接暴露。
 */
export async function openHarness({
	dbPath,
	tokensPerSecond,
	registry,
	settings,
	now,
	setTimer,
}: OpenHarnessOptions): Promise<OpenedHarness> {
	const faux = fauxProvider(tokensPerSecond === undefined ? undefined : { tokensPerSecond });
	const models = createModels();
	models.setProvider(faux.provider);

	const storage = await openNodeSqliteStorage(dbPath);
	const harness = await Harness.open(
		storage,
		{
			models,
			registry: registry ?? createRegistry(),
			...(settings === undefined ? {} : { settings }),
			...(now === undefined ? {} : { now }),
			...(setTimer === undefined ? {} : { setTimer }),
		},
		BACKGROUND_CONTEXT,
	);

	return {
		harness,
		faux,
		close: async () => {
			await harness.close(BACKGROUND_CONTEXT);
		},
	};
}

/** 让 faux 在下一轮返回一句纯文本回答。所有 probe 都通过它固定模型行为。 */
export function queueTextAnswer(faux: FauxProviderHandle, text: string): void {
	faux.setResponses([fauxAssistantMessage([fauxText(text)])]);
}

/** 故意长：配合 faux 的 tokensPerSecond 把这一轮流式拉成数秒，好让 kill 落在流中间。 */
export const LONG_ANSWER =
	"我用一段比较长的回答来演示流式输出：这一轮会逐字推送，进程在推送过程中被强杀，" +
	"重启后 durable 会把这次请求算作一次失败尝试，并按 retry 策略重发，而不是把半个回答留在记忆里。";

/**
 * 装一个"永远在线"的模型脚本：faux 的脚本步是一次性 shift 掉的，而脚本只活在进程内存里——
 * 进程被杀后重启，队列就是空的，durable 的重试会直接拿到 model_error。真实模型不会这样：
 * 进程死了，外部模型还在。所以这个工厂在每次被取用时把自己重新排回队尾，
 * 等价于"外部世界不随我们崩溃"，这正是 durable 恢复语义成立的前提。
 */
export function installAnswerScript(faux: FauxProviderHandle): void {
	const answer: FauxResponseFactory = (context) => {
		faux.appendResponses([answer]);
		const lastUser = [...context.messages].reverse().find((message) => message.role === "user");
		const text = typeof lastUser?.content === "string" ? lastUser.content : "";
		return fauxAssistantMessage([fauxText(text.includes("长回答") ? LONG_ANSWER : `收到：${text}`)]);
	};
	faux.setResponses([answer]);
}
