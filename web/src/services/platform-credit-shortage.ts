import { ApiError } from "@/services/api/request";
import { getCredits } from "@/services/api/platform";

export type PlatformCreditShortage = {
    requiredCredits: number;
    requestCount: number;
    balanceState: "loading" | "ready" | "unavailable";
    availableCredits?: number;
};

type Listener = (notice: PlatformCreditShortage | undefined) => void;
type Entry = { requiredCredits: number; release: () => void };
let listener: Listener | undefined;
let notice: PlatformCreditShortage | undefined;
let balanceController: AbortController | undefined;
const pending = new Map<string, Entry>();
const handledErrors = new WeakSet<ApiError>();

export function isPlatformCreditShortageError(error: unknown): error is ApiError {
    return error instanceof ApiError && error.reason === "insufficient_credits";
}

/** 只有专用提示已接管的错误才跳过通用提示，保持原始准入拒绝语义。 */
export function isPlatformCreditShortageHandled(error: unknown): error is ApiError {
    return isPlatformCreditShortageError(error) && handledErrors.has(error);
}

export function dismissPlatformCreditShortage() {
    balanceController?.abort();
    balanceController = undefined;
    for (const entry of pending.values()) entry.release();
    pending.clear();
    notice = undefined;
    listener?.(notice);
}

export function subscribePlatformCreditShortages(next: Listener) {
    listener = next;
    next(notice);
    return () => {
        if (listener !== next) return;
        listener = undefined;
        dismissPlatformCreditShortage();
    };
}

function refreshBalance() {
    balanceController?.abort();
    const controller = new AbortController();
    balanceController = controller;
    notice = {
        requiredCredits: [...pending.values()].reduce((total, entry) => total + entry.requiredCredits, 0),
        requestCount: pending.size,
        balanceState: "loading",
    };
    listener?.(notice);
    void getCredits({ signal: controller.signal }).then((account) => {
        if (controller.signal.aborted || !notice) return;
        const available = account.available;
        notice = typeof available === "number" && Number.isSafeInteger(available) && available >= 0
            ? { ...notice, balanceState: "ready", availableCredits: available }
            : { ...notice, balanceState: "unavailable" };
        listener?.(notice);
    }).catch(() => {
        if (controller.signal.aborted || !notice) return;
        notice = { ...notice, balanceState: "unavailable" };
        listener?.(notice);
    });
}

/** 提示立即接管错误；余额读取和关闭弹窗都不阻塞调用方释放生成状态。 */
export function notifyPlatformCreditShortage(error: ApiError, requiredCredits: number, operationId: string, signal?: AbortSignal) {
    if (!listener || signal?.aborted || !isPlatformCreditShortageError(error)) return;
    handledErrors.add(error);
    if (pending.has(operationId)) return;
    const cancel = () => {
        pending.get(operationId)?.release();
        pending.delete(operationId);
        if (pending.size) refreshBalance();
        else dismissPlatformCreditShortage();
    };
    pending.set(operationId, { requiredCredits, release: () => signal?.removeEventListener("abort", cancel) });
    signal?.addEventListener("abort", cancel, { once: true });
    refreshBalance();
}
