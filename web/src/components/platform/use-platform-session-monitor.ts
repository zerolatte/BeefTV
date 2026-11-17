import { useEffect } from "react";

import { getPlatformAuthMe } from "@/services/api/platform-auth";
import { ApiError } from "@/services/api/request";
import { captureWorkspaceSession, invalidateWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";

export function usePlatformSessionMonitor(enabled: boolean) {
    useEffect(() => {
        if (!enabled) return;
        let controller: AbortController | undefined;
        let disposed = false;

        const check = async () => {
            if (disposed || document.visibilityState !== "visible" || controller) return;
            const session = captureWorkspaceSession();
            if (session.identity?.mode !== "platform") return;
            const pending = new AbortController();
            controller = pending;
            try {
                const grant = await getPlatformAuthMe({ signal: pending.signal, timeout: 2_000 });
                if (disposed || pending.signal.aborted || !isWorkspaceSessionCurrent(session)) return;
                const identity = session.identity;
                if (grant.account.status !== "active" || grant.account.mustChangePassword || grant.account.id !== identity.accountId || grant.account.workspaceId !== identity.workspaceId || grant.sessionId !== identity.sessionId) {
                    invalidateWorkspaceSession("session_changed");
                }
            } catch (failure) {
                if (disposed || pending.signal.aborted || !isWorkspaceSessionCurrent(session)) return;
                if (failure instanceof ApiError && failure.status === 401) invalidateWorkspaceSession("session_expired");
                // 短暂断网不能证明会话失效，保留草稿并在页面可见时重试。
            } finally {
                if (controller === pending) controller = undefined;
            }
        };
        const visibilityChanged = () => {
            if (document.visibilityState === "visible") void check();
            else controller?.abort();
        };
        const focused = () => { void check(); };
        const interval = window.setInterval(() => void check(), 2_500);
        document.addEventListener("visibilitychange", visibilityChanged);
        window.addEventListener("focus", focused);
        void check();
        return () => {
            disposed = true;
            controller?.abort();
            window.clearInterval(interval);
            document.removeEventListener("visibilitychange", visibilityChanged);
            window.removeEventListener("focus", focused);
        };
    }, [enabled]);
}
