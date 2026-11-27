import { afterEach, expect, test } from "bun:test";
import { ApiError, apiClient } from "../src/services/api/request";
import { createGenerationTask } from "../src/services/api/task-center";
import { resetWorkspaceMode, setWorkspaceCapabilitySnapshot } from "../src/services/workspace-mode";
import { subscribePlatformCreditShortages, isPlatformCreditShortageHandled, type PlatformCreditShortage } from "../src/services/platform-credit-shortage";
import { submitGenerationCommand } from "../src/services/api/generation-submission";

const original = apiClient.request;
let stop: (() => void) | undefined;
afterEach(() => { apiClient.request = original; stop?.(); stop = undefined; resetWorkspaceMode(); });
function platform() {
    setWorkspaceCapabilitySnapshot({ contractVersion: 1, profile: "platform", capabilities: { localAssets: true, providerCalls: true } });
}
const request = { type: "canvas_text", prompt: "测试", input: { mode: "text", config: { channelId: "channel", model: "text", baseUrl: "https://example.invalid", apiKey: "synthetic-test-key" }, metadata: { clientOperationId: "operation" } } };
const quote = { id: "quote", units: 1, unitCredits: 12, totalCredits: 12, expiresAt: new Date(Date.now() + 60000).toISOString(), priceVersion: 1 };
const task = { id: "accepted-task", type: "canvas_text", status: "queued", prompt: "测试", attempts: 0, createdAt: "2026-01-01", updatedAt: "2026-01-01" };
const response = (data: unknown, status = 200, reason?: string) => ({ data: { code: status === 200 ? 0 : status, data, msg: "测试响应", reason }, status });
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test("平台报价后直接提交，不需要确认或余额预检，报价和提交不携带浏览器凭据", async () => {
    platform();
    const calls: string[] = [];
    apiClient.request = (async (config: { url: string; data?: Record<string, unknown> }) => {
        calls.push(config.url);
        if (config.url.includes("by-operation")) return { data: { code: 404, data: null, msg: "不存在" }, status: 404 };
        expect((config.data?.input as { config: unknown }).config).toEqual({ channelId: "channel", model: "text" });
        if (config.url === "/tasks/quote") return response(quote);
        expect(config.url).toBe("/tasks");
        expect(config.data?.quoteId).toBe("quote");
        return response(task);
    }) as typeof apiClient.request;
    expect(await createGenerationTask(request)).toEqual(task);
    expect(calls).toEqual(["/tasks/by-operation/operation", "/tasks/quote", "/tasks"]);
    expect(request.input.config.apiKey).toBe("synthetic-test-key");
});

test("已有提交仍由后端核对内容冲突，不重新报价", async () => {
    platform();
    const calls: string[] = [];
    apiClient.request = (async (config: { url: string }) => {
        calls.push(config.url);
        if (config.url.includes("by-operation")) return { data: { code: 0, data: { id: "existing-task" } }, status: 200 };
        return { data: { code: 409, data: null, msg: "提交内容已变化", reason: "conflict" }, status: 409 };
    }) as typeof apiClient.request;
    const error = await createGenerationTask(request).catch((error: unknown) => error);
    expect(error).toMatchObject({ status: 409, reason: "conflict" });
    expect(calls).toEqual(["/tasks/by-operation/operation", "/tasks"]);
});

test("事务返回积分不足后才读取最新余额，保留原始拒绝且不重发任务", async () => {
    platform();
    const calls: string[] = [];
    const rejected = new ApiError("积分不足", { status: 403, reason: "insufficient_credits" });
    let notice: PlatformCreditShortage | undefined;
    stop = subscribePlatformCreditShortages((value) => { notice = value; });
    apiClient.request = (async (config: { url: string }) => {
        calls.push(config.url);
        if (config.url.includes("by-operation")) return response(null, 404);
        if (config.url === "/tasks/quote") return response(quote);
        if (config.url === "/credits") return response({ available: 3, frozen: 9 });
        throw rejected;
    }) as typeof apiClient.request;
    let recoveryReads = 0;
    const error = await submitGenerationCommand(request, "operation", createGenerationTask, async () => { recoveryReads++; return undefined; }).catch((error) => error);
    await settle();
    expect(error).toBe(rejected);
    expect(isPlatformCreditShortageHandled(error)).toBe(true);
    expect(notice).toEqual({ requiredCredits: 12, requestCount: 1, availableCredits: 3, balanceState: "ready" });
    expect(calls).toEqual(["/tasks/by-operation/operation", "/tasks/quote", "/tasks", "/credits"]);
    expect(recoveryReads).toBe(0);
});

for (const failure of [
    new ApiError("报价已过期", { status: 409, reason: "quote_expired" }),
    new ApiError("余额不足的普通文案", { status: 403, reason: "forbidden" }),
    new ApiError("网络中断", { reason: "network_error" }),
    new DOMException("已取消", "AbortError"),
]) test(`${failure.message}不显示积分不足，也不在报价提交入口自动重发`, async () => {
    platform();
    let notices = 0;
    let submits = 0;
    stop = subscribePlatformCreditShortages((value) => { if (value) notices++; });
    apiClient.request = (async (config: { url: string }) => {
        if (config.url.includes("by-operation")) return response(null, 404);
        if (config.url === "/tasks/quote") return response(quote);
        expect(config.url).toBe("/tasks");
        submits++;
        throw failure;
    }) as typeof apiClient.request;
    await expect(createGenerationTask(request)).rejects.toBe(failure);
    expect(submits).toBe(1);
    expect(notices).toBe(0);
});

test("报价阶段失败不提交任务；报价后取消也不会发起任务", async () => {
    platform();
    for (const cancel of [false, true]) {
        const controller = new AbortController();
        const calls: string[] = [];
        apiClient.request = (async (config: { url: string }) => {
            calls.push(config.url);
            if (config.url.includes("by-operation")) return response(null, 404);
            if (!cancel) throw new ApiError("价格尚未发布", { status: 422, reason: "price_unavailable" });
            controller.abort();
            return response(quote);
        }) as typeof apiClient.request;
        await expect(createGenerationTask(request, { signal: controller.signal })).rejects.toThrow();
        expect(calls).toEqual(["/tasks/by-operation/operation", "/tasks/quote"]);
    }
});

test("本地模式沿用直接提交且保留本地连接配置", async () => {
    setWorkspaceCapabilitySnapshot({ contractVersion: 1, profile: "local", capabilities: { localAssets: true, providerCalls: true } });
    apiClient.request = (async (config: { url: string; data: unknown }) => {
        expect(config.url).toBe("/tasks");
        expect(config.data).toEqual(request);
        return response(task);
    }) as typeof apiClient.request;
    expect(await createGenerationTask(request)).toEqual(task);
});
