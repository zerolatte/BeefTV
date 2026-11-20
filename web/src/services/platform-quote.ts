import type { CreditQuote } from "@/services/api/platform";

type Request = { quote: CreditQuote; resolve: (confirmed: boolean) => void };
let listener: ((requests: Request[]) => void) | undefined;
const pending = new Set<Request>();
const publish = () => listener?.([...pending]);
export function subscribePlatformQuotes(next: (requests: Request[]) => void) {
    listener = next; publish();
    return () => { listener = undefined; for (const request of [...pending]) request.resolve(false); };
}
export function confirmPlatformQuote(quote: CreditQuote, signal?: AbortSignal) {
    if (!listener) return Promise.reject(new Error("积分确认界面尚未就绪，请稍后重试"));
    return new Promise<void>((resolve, reject) => {
        const cancel = () => request.resolve(false);
        const request: Request = { quote, resolve: (confirmed) => {
            if (!pending.delete(request)) return;
            signal?.removeEventListener("abort", cancel); publish();
            if (!confirmed || signal?.aborted) reject(new DOMException("已取消生成", "AbortError"));
            else if (Date.parse(quote.expiresAt) <= Date.now()) reject(new Error("报价已过期，请重新生成并确认报价"));
            else resolve();
        } };
        pending.add(request); signal?.addEventListener("abort", cancel, { once: true });
        if (signal?.aborted) cancel(); else publish();
    });
}
