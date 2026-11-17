export type PlatformSessionNotice = {
    kind: "changed" | "logout";
    accountId: string;
    workspaceId: string;
    sessionId: string;
};

const CHANNEL_NAME = "beeftv-platform-session";
const STORAGE_EVENT_KEY = "beeftv-platform-session-notice";

function validNotice(value: unknown): value is PlatformSessionNotice {
    if (!value || typeof value !== "object") return false;
    const candidate = value as Partial<PlatformSessionNotice>;
    return (candidate.kind === "changed" || candidate.kind === "logout")
        && typeof candidate.accountId === "string" && candidate.accountId.length > 0
        && typeof candidate.workspaceId === "string" && candidate.workspaceId.length > 0
        && typeof candidate.sessionId === "string" && candidate.sessionId.length > 0;
}

export function createPlatformSessionChannel(onNotice: (notice: PlatformSessionNotice) => void) {
    const channel = typeof BroadcastChannel === "function" ? new BroadcastChannel(CHANNEL_NAME) : null;
    if (channel) channel.onmessage = (event: MessageEvent<unknown>) => {
        if (validNotice(event.data)) onNotice(event.data);
    };
    const onStorage = (event: StorageEvent) => {
        if (event.key !== STORAGE_EVENT_KEY || !event.newValue) return;
        try {
            const envelope = JSON.parse(event.newValue) as { notice?: unknown };
            if (validNotice(envelope.notice)) onNotice(envelope.notice);
        } catch {
            // 其他页面写入的无效事件不得改变当前身份。
        }
    };
    if (!channel && typeof window !== "undefined") window.addEventListener("storage", onStorage);

    return {
        publish(notice: PlatformSessionNotice) {
            if (!validNotice(notice)) throw new Error("平台会话通知身份不完整");
            if (channel) channel.postMessage(notice);
            else if (typeof window !== "undefined") {
                window.localStorage.setItem(STORAGE_EVENT_KEY, JSON.stringify({ nonce: crypto.randomUUID(), notice }));
                window.localStorage.removeItem(STORAGE_EVENT_KEY);
            }
        },
        close() {
            channel?.close();
            if (!channel && typeof window !== "undefined") window.removeEventListener("storage", onStorage);
        },
    };
}
