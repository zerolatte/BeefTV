import { afterEach, expect, test } from "bun:test";
import { apiClient } from "../src/services/api/request";
import { createGenerationTask } from "../src/services/api/task-center";
import { resetWorkspaceMode, setWorkspaceCapabilitySnapshot } from "../src/services/workspace-mode";
import { subscribePlatformQuotes } from "../src/services/platform-quote";

const original = apiClient.request;
let stop: (() => void) | undefined;
afterEach(() => { apiClient.request = original; stop?.(); stop = undefined; resetWorkspaceMode(); });
function platform() {
    setWorkspaceCapabilitySnapshot({ contractVersion: 1, profile: "platform", capabilities: { localAssets: true, providerCalls: true } });
}
const request = { type: "canvas_text", prompt: "测试", input: { mode: "text", config: { channelId: "channel", model: "text", baseUrl: "https://example.invalid", apiKey: "synthetic-test-key" }, metadata: { clientOperationId: "operation" } } };

test("平台取消报价不创建任务，报价不携带浏览器凭据", async () => {
    platform();
    const calls: string[] = [];
    apiClient.request = (async (config: { url: string; data?: Record<string, unknown> }) => {
        calls.push(config.url);
        if (config.url.includes("by-operation")) return { data: { code: 404, data: null, msg: "不存在" }, status: 404 };
        expect(config.url).toBe("/tasks/quote");
        expect((config.data?.input as { config: unknown }).config).toEqual({ channelId: "channel", model: "text" });
        return { data: { code: 0, data: { id: "quote", units: 1, unitCredits: 2, totalCredits: 2, expiresAt: new Date(Date.now() + 60000).toISOString(), priceVersion: 1 } }, status: 200 };
    }) as typeof apiClient.request;
    stop = subscribePlatformQuotes((quotes) => { quotes[0]?.resolve(false); });
    const error = await createGenerationTask(request).catch((error: unknown) => error);
    expect(error).toMatchObject({ name: "AbortError" });
    expect(calls).toEqual(["/tasks/by-operation/operation", "/tasks/quote"]);
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
