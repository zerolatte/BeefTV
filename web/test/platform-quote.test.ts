import { afterEach, expect, test } from "bun:test";
import { confirmPlatformQuote, subscribePlatformQuotes } from "../src/services/platform-quote";

let stop: (() => void) | undefined;
afterEach(() => { stop?.(); stop = undefined; });
const quote = { id: "quote", unitCredits: 2, units: 1, totalCredits: 2, priceVersion: 1, expiresAt: new Date(Date.now() + 60000).toISOString() };

test("取消请求关闭待确认报价，不能继续提交", async () => {
    let pending: Parameters<Parameters<typeof subscribePlatformQuotes>[0]>[0] = [];
    stop = subscribePlatformQuotes((requests) => { pending = requests; });
    const controller = new AbortController();
    const result = confirmPlatformQuote(quote, controller.signal).catch((error: unknown) => error);
    expect(pending.length).toBe(1);
    controller.abort();
    expect(await result).toMatchObject({ name: "AbortError" });
    expect(pending.length).toBe(0);
});

test("界面卸载时取消全部未确认报价", async () => {
    stop = subscribePlatformQuotes(() => {});
    const first = confirmPlatformQuote(quote).catch((error: unknown) => error);
    const second = confirmPlatformQuote(quote).catch((error: unknown) => error);
    stop(); stop = undefined;
    for (const result of await Promise.all([first, second])) expect(result).toMatchObject({ name: "AbortError" });
});

test("过期报价不能确认", async () => {
    let confirm: (() => void) | undefined;
    stop = subscribePlatformQuotes((requests) => { if (requests[0]) confirm = () => requests[0].resolve(true); });
    const result = confirmPlatformQuote({ ...quote, expiresAt: new Date(0).toISOString() }).catch((error: unknown) => error);
    confirm?.();
    expect(await result).toMatchObject({ message: "报价已过期，请重新生成并确认报价" });
});
