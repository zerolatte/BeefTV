import { afterEach, expect, test } from "bun:test";
import { apiClient, ApiError } from "../src/services/api/request";
import { dismissPlatformCreditShortage, isPlatformCreditShortageHandled, notifyPlatformCreditShortage, subscribePlatformCreditShortages, type PlatformCreditShortage } from "../src/services/platform-credit-shortage";

const original = apiClient.request;
let stop: (() => void) | undefined;
let notice: PlatformCreditShortage | undefined;
const rejected = () => new ApiError("积分不足", { status: 403, reason: "insufficient_credits" });
const response = (available: unknown) => ({ data: { code: 0, data: { available, frozen: 0 } }, status: 200 });
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
function listen() { stop = subscribePlatformCreditShortages((next) => { notice = next; }); }
afterEach(() => { stop?.(); stop = undefined; apiClient.request = original; notice = undefined; });

test("无提示宿主时保留通用错误处理，不静默吞掉拒绝", () => {
    const error = rejected();
    notifyPlatformCreditShortage(error, 10, "operation");
    expect(isPlatformCreditShortageHandled(error)).toBe(false);
});

test("不足立即交还控制权，同批拒绝合并，重复回执不重复累计", async () => {
    listen();
    apiClient.request = (async () => response(4)) as typeof apiClient.request;
    const first = rejected();
    notifyPlatformCreditShortage(first, 10, "first");
    expect(isPlatformCreditShortageHandled(first)).toBe(true);
    expect(notice?.balanceState).toBe("loading");
    notifyPlatformCreditShortage(rejected(), 10, "first");
    notifyPlatformCreditShortage(rejected(), 5, "second");
    await settle();
    expect(notice).toEqual({ requiredCredits: 15, requestCount: 2, balanceState: "ready", availableCredits: 4 });
    dismissPlatformCreditShortage();
    expect(notice).toBeUndefined();
});

for (const balance of [undefined, -1, "0", 0.5]) test(`无效余额 ${String(balance)} 不伪造为零`, async () => {
    listen();
    apiClient.request = (async () => response(balance)) as typeof apiClient.request;
    notifyPlatformCreditShortage(rejected(), 10, "operation");
    await settle();
    expect(notice).toEqual({ requiredCredits: 10, requestCount: 1, balanceState: "unavailable" });
});

test("余额读取失败仍保留所需积分", async () => {
    listen();
    apiClient.request = (async () => { throw new Error("离线"); }) as typeof apiClient.request;
    notifyPlatformCreditShortage(rejected(), 10, "operation");
    await settle();
    expect(notice).toEqual({ requiredCredits: 10, requestCount: 1, balanceState: "unavailable" });
});

test("关闭、取消或卸载后不回写旧余额，最新一批使用新的余额", async () => {
    listen();
    const pending: Array<{ resolve: (value: ReturnType<typeof response>) => void; signal: AbortSignal }> = [];
    apiClient.request = ((config: { signal: AbortSignal }) => new Promise((resolve) => pending.push({ resolve, signal: config.signal }))) as typeof apiClient.request;
    const controller = new AbortController();
    notifyPlatformCreditShortage(rejected(), 10, "first", controller.signal);
    controller.abort();
    expect(notice).toBeUndefined();
    expect(pending[0].signal.aborted).toBe(true);
    notifyPlatformCreditShortage(rejected(), 6, "second");
    pending[1].resolve(response(1));
    await settle();
    pending[0].resolve(response(100));
    await settle();
    expect(notice?.availableCredits).toBe(1);
    dismissPlatformCreditShortage();
    notifyPlatformCreditShortage(rejected(), 7, "third");
    stop?.();
    expect(pending[2].signal.aborted).toBe(true);
    pending[2].resolve(response(99));
    listen();
    await settle();
    expect(notice).toBeUndefined();
});

test("已取消的提交不打开提示或读取余额", () => {
    listen();
    apiClient.request = (() => { throw new Error("不应读取余额"); }) as typeof apiClient.request;
    notifyPlatformCreditShortage(rejected(), 10, "cancelled", AbortSignal.abort());
    expect(notice).toBeUndefined();
});
