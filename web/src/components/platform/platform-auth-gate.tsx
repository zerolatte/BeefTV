import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Button } from "antd";

import { PlatformAuthScreen } from "@/components/platform/platform-auth-screen";
import { usePlatformSessionMonitor } from "@/components/platform/use-platform-session-monitor";
import { FullScreenLoader } from "@/components/ui/aceternity/full-screen-loader";
import { AppModal } from "@/components/ui/product/app-modal";
import { applyPlatformUserSession } from "@/lib/user-session";
import { platformWorkspaceStorageScope, setActiveUserScope } from "@/lib/user-scope";
import {
    changePlatformPassword,
    clearPlatformCsrfToken,
    getPlatformAuthMe,
    getPlatformWorkspaceBootstrap,
    loginPlatform,
    logoutPlatform,
    type PlatformSessionGrant,
} from "@/services/api/platform-auth";
import { ApiError } from "@/services/api/request";
import { activatePlatformWorkspaceCacheScope, clearPlatformWorkspaceCache, flushPlatformDrafts, inspectPlatformDrafts } from "@/services/platform-workspace-cleanup";
import { withGenerationConsumersPaused } from "@/services/generation-consumer-lifecycle";
import { createPlatformSessionChannel } from "@/services/platform-session-channel";
import { activateWorkspaceSession, captureWorkspaceSession, invalidateWorkspaceSession, subscribeWorkspaceSession } from "@/services/workspace-session";
import { useUserStore } from "@/stores/use-user-store";

const PlatformAuthContext = createContext<{ requestLogout: () => void } | null>(null);

export function usePlatformAuthActions() {
    return useContext(PlatformAuthContext);
}

type ExitAction = "logout" | "abandon";

export function PlatformAuthGate({ children }: { children: ReactNode }) {
    const authState = useUserStore((state) => state.authState);
    const user = useUserStore((state) => state.user);
    const [workspaceMounted, setWorkspaceMounted] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string>();
    const [loading, setLoading] = useState(true);
    const [startupFailed, setStartupFailed] = useState(false);
    const [unsyncedDraftCount, setUnsyncedDraftCount] = useState(0);
    const [exitAction, setExitAction] = useState<ExitAction | null>(null);
    const [exitError, setExitError] = useState<string>();
    const [recoveryAuthenticated, setRecoveryAuthenticated] = useState(false);
    const [cleanupLocked, setCleanupLocked] = useState(false);
    const operationRef = useRef(false);
    const mountedRef = useRef(false);
    const channelRef = useRef<ReturnType<typeof createPlatformSessionChannel> | null>(null);

    usePlatformSessionMonitor(workspaceMounted && authState === "authenticated" && !exitAction && !cleanupLocked);

    const workspaceScope = useCallback(() => {
        const workspaceId = useUserStore.getState().user?.workspaceId;
        if (!workspaceId) throw new Error("当前平台工作区身份不完整，请重新登录。");
        return platformWorkspaceStorageScope(workspaceId);
    }, []);

    const inspectDrafts = useCallback(async () => {
        if (!useUserStore.getState().user?.workspaceId) return;
        const inspection = await inspectPlatformDrafts(workspaceScope());
        setUnsyncedDraftCount(inspection.hasUnsynced ? Math.max(1, inspection.drafts.length) : 0);
    }, [workspaceScope]);

    const resumeWorkspace = useCallback(async () => {
        await flushPlatformDrafts({ scope: workspaceScope() });
        setRecoveryAuthenticated(false);
        setError(undefined);
        useUserStore.getState().setAuthState("authenticated");
    }, [workspaceScope]);

    const acceptGrant = useCallback(async (grant: PlatformSessionGrant) => {
        const previous = useUserStore.getState().user;
        const account = grant.account;
        if (account.status !== "active" || !account.id || !account.workspaceId || !grant.sessionId) {
            throw new Error("会话身份无效，请重新登录。");
        }
        if ((mountedRef.current || useUserStore.getState().authState === "reauth-required") && (previous?.id !== account.id || previous.workspaceId !== account.workspaceId)) {
            throw new Error("当前仍有原账号的工作区内容。请先重新认证原账号并同步，或明确放弃后再切换账号。");
        }

        activatePlatformWorkspaceCacheScope(platformWorkspaceStorageScope(account.workspaceId));
        activateWorkspaceSession({ mode: "platform", accountId: account.id, workspaceId: account.workspaceId, sessionId: grant.sessionId });
        useUserStore.getState().setUser({ ...account, status: "active" });
        useUserStore.getState().setStorageMode("remote");

        if (account.mustChangePassword) {
            useUserStore.getState().setAuthState("password-change-required");
            return;
        }
        if (mountedRef.current) {
            useUserStore.getState().setAuthState("reauth-required");
            setRecoveryAuthenticated(true);
            await resumeWorkspace();
            return;
        }

        try {
            const bootstrap = await getPlatformWorkspaceBootstrap();
            if (bootstrap.account.id !== account.id || bootstrap.workspace.id !== account.workspaceId || bootstrap.sessionId !== grant.sessionId) {
                throw new Error("工作区引导时会话发生变化，请重新登录。");
            }
            await applyPlatformUserSession(bootstrap);
            mountedRef.current = true;
            setWorkspaceMounted(true);
            setStartupFailed(false);
        } catch (failure) {
            useUserStore.getState().setAuthState("reauth-required", "bootstrap_incomplete");
            throw failure;
        }
    }, [resumeWorkspace]);

    const loadSession = useCallback(async (isCancelled: () => boolean = () => false) => {
        setLoading(true);
        setError(undefined);
        setStartupFailed(false);
        try {
            const grant = await getPlatformAuthMe();
            if (isCancelled()) return;
            await acceptGrant(grant);
        } catch (failure) {
            if (isCancelled()) return;
            if (failure instanceof ApiError && failure.status === 401) {
                useUserStore.getState().setAuthState("anonymous");
            } else {
                setError(errorMessage(failure));
                setStartupFailed(useUserStore.getState().authState !== "reauth-required");
            }
        } finally {
            if (!isCancelled()) setLoading(false);
        }
    }, [acceptGrant]);

    useEffect(() => {
        let cancelled = false;
        void loadSession(() => cancelled);
        return () => { cancelled = true; };
    }, [loadSession]);

    useEffect(() => {
        const channel = createPlatformSessionChannel((notice) => {
            const current = captureWorkspaceSession().identity;
            if (!current || current.mode !== "platform") return;
            if (notice.kind === "changed" && notice.sessionId === current.sessionId) return;
            if (notice.kind === "logout" && notice.sessionId !== current.sessionId) return;
            invalidateWorkspaceSession(notice.kind === "logout" ? "cross_tab_logout" : "cross_tab_session_changed");
        });
        channelRef.current = channel;
        const unsubscribe = subscribeWorkspaceSession((session) => {
            if (!session.identity) setRecoveryAuthenticated(false);
        });
        return () => { unsubscribe(); channelRef.current = null; channel.close(); };
    }, []);

    const acceptChangedGrant = async (grant: PlatformSessionGrant) => {
        channelRef.current?.publish({ kind: "changed", accountId: grant.account.id, workspaceId: grant.account.workspaceId, sessionId: grant.sessionId });
        await acceptGrant(grant);
    };

    useEffect(() => {
        if (authState !== "reauth-required") return;
        void inspectDrafts().catch((failure) => setError(errorMessage(failure)));
    }, [authState, inspectDrafts]);

    const runAuthOperation = async (operation: () => Promise<void>) => {
        if (operationRef.current) return;
        operationRef.current = true;
        setBusy(true);
        setError(undefined);
        try {
            await operation();
        } catch (failure) {
            setError(errorMessage(failure));
        } finally {
            operationRef.current = false;
            setBusy(false);
        }
    };

    const requestLogout = useCallback(() => {
        setExitError(undefined);
        setExitAction("logout");
        void inspectDrafts().catch((failure) => setExitError(errorMessage(failure)));
    }, [inspectDrafts]);

    const finishExit = async (discard: boolean) => {
        if (operationRef.current) return;
        operationRef.current = true;
        setBusy(true);
        setExitError(undefined);
        try {
            await withGenerationConsumersPaused(async () => {
                const scope = workspaceScope();
                const exitingIdentity = captureWorkspaceSession().identity;
                if (!discard) await flushPlatformDrafts({ scope });
                if (exitingIdentity) {
                    try {
                        await logoutPlatform();
                    } catch (failure) {
                        if (!(failure instanceof ApiError && failure.status === 401)) throw failure;
                    }
                }
                setCleanupLocked(true);
                invalidateWorkspaceSession("logout");
                if (exitingIdentity?.accountId && exitingIdentity.sessionId) {
                    channelRef.current?.publish({ kind: "logout", accountId: exitingIdentity.accountId, workspaceId: exitingIdentity.workspaceId, sessionId: exitingIdentity.sessionId });
                }
                await clearPlatformWorkspaceCache(scope);
                clearPlatformCsrfToken();
                mountedRef.current = false;
                setWorkspaceMounted(false);
                useUserStore.getState().clearSession();
                useUserStore.getState().setHydrated(false);
                setActiveUserScope(null);
                setExitAction(null);
                setRecoveryAuthenticated(false);
                setCleanupLocked(false);
                setError(undefined);
            });
        } catch (failure) {
            setExitError(errorMessage(failure));
            await inspectDrafts().catch(() => undefined);
        } finally {
            operationRef.current = false;
            setBusy(false);
        }
    };

    const recovery = authState === "reauth-required";
    const blocked = authState !== "authenticated" || Boolean(exitAction) || cleanupLocked;
    const showAuth = !loading && !startupFailed && authState !== "authenticated";

    return (
        <PlatformAuthContext.Provider value={{ requestLogout }}>
            {workspaceMounted ? (
                <div inert={blocked} aria-hidden={blocked || undefined} style={{ visibility: blocked ? "hidden" : undefined }}>
                    {children}
                </div>
            ) : null}

            {blocked ? <PortaledWorkspaceBlocker /> : null}

            {loading ? <FullScreenLoader label="正在验证平台会话" detail="" /> : null}
            {startupFailed ? (
                <div className="app-user-workspace grid min-h-dvh place-items-center p-5" style={{ background: "var(--user-page-bg)" }}>
                    <div className="grid max-w-lg gap-4" role="alert">
                        <p className="text-[14px] leading-[22px]" style={{ color: "var(--user-ink)" }}>{error}</p>
                        <Button onClick={() => void loadSession()}>重新连接</Button>
                    </div>
                </div>
            ) : null}

            {showAuth ? (
                <div className={workspaceMounted ? "fixed inset-0 overflow-y-auto" : undefined} style={workspaceMounted ? { zIndex: "var(--z-modal)" } : undefined} role={workspaceMounted ? "dialog" : undefined} aria-modal={workspaceMounted || undefined} aria-label={workspaceMounted ? "恢复工作区会话" : undefined}>
                    {recovery && recoveryAuthenticated ? (
                        <div className="app-user-workspace grid min-h-dvh place-items-center p-5" style={{ background: "var(--user-page-bg)" }}>
                            <div className="grid max-w-lg gap-4">
                                <p className="text-[14px] leading-[22px]" style={{ color: "var(--user-ink)" }}>已验证原账号，请继续同步未保存的内容。</p>
                                {error ? <p role="alert" className="text-[13px] leading-5" style={{ color: "var(--palette-status-error)" }}>{error}</p> : null}
                                <Button type="primary" loading={busy} onClick={() => void runAuthOperation(resumeWorkspace)}>继续同步</Button>
                                <Button danger disabled={busy} onClick={() => setExitAction("abandon")}>放弃本地未同步内容并切换账号</Button>
                            </div>
                        </div>
                    ) : (
                        <PlatformAuthScreen
                            key={`${authState}:${user?.id || "anonymous"}`}
                            mode={authState === "password-change-required" ? "password-change" : recovery ? "session-recovery" : "login"}
                            username={user?.username}
                            busy={busy}
                            error={error}
                            unsyncedDraftCount={unsyncedDraftCount}
                            onLogin={(username, password) => void runAuthOperation(async () => acceptChangedGrant(await loginPlatform(username, password)))}
                            onChangePassword={(oldPassword, newPassword) => void runAuthOperation(async () => acceptChangedGrant(await changePlatformPassword(oldPassword, newPassword)))}
                            onReauthenticate={(username, password) => void runAuthOperation(async () => acceptChangedGrant(await loginPlatform(username, password)))}
                            onAbandonDrafts={recovery ? () => setExitAction("abandon") : undefined}
                        />
                    )}
                </div>
            ) : null}

            <AppModal
                open={Boolean(exitAction)}
                title={exitAction === "abandon" ? "放弃未同步内容并切换账号？" : "退出当前账号？"}
                onCancel={() => { if (!busy && !cleanupLocked) setExitAction(null); }}
                closable={!busy && !cleanupLocked}
                maskClosable={!busy && !cleanupLocked}
                footer={(
                    <div className="flex flex-wrap justify-end gap-2">
                        {!cleanupLocked ? <Button disabled={busy} onClick={() => setExitAction(null)}>返回工作区</Button> : null}
                        {exitAction === "logout" && !cleanupLocked ? <Button type="primary" loading={busy} onClick={() => void finishExit(false)}>同步并退出</Button> : null}
                        {exitAction === "abandon" || exitError ? <Button danger loading={busy} onClick={() => void finishExit(true)}>确认放弃并退出</Button> : null}
                    </div>
                )}
            >
                <p data-platform-auth-exit className="text-[14px] leading-[22px]" style={{ color: "var(--user-ink)" }}>
                    {exitAction === "abandon"
                        ? "本机尚未同步到服务器的修改会被移除。此操作确认后才会允许切换其他账号。"
                        : unsyncedDraftCount > 0
                            ? `有 ${unsyncedDraftCount} 项内容尚未确认同步，退出前将尝试保存到服务器。`
                            : "退出前将确认本地编辑已经同步到服务器。"}
                </p>
                {exitError ? <p role="alert" className="mt-3 text-[13px] leading-5" style={{ color: "var(--palette-status-error)" }}>{exitError}</p> : null}
            </AppModal>
        </PlatformAuthContext.Provider>
    );
}

function errorMessage(failure: unknown) {
    return failure instanceof Error ? failure.message : "操作未完成，请重试。";
}

function PortaledWorkspaceBlocker() {
    useEffect(() => {
        const previous = new Map<HTMLElement, { inert: boolean; visibility: string }>();
        const block = () => {
            for (const element of document.body.querySelectorAll<HTMLElement>(".ant-modal-root, .ant-drawer-root, .ant-popover, .ant-dropdown")) {
                if (element.querySelector("[data-platform-auth-exit]")) continue;
                if (!previous.has(element)) previous.set(element, { inert: element.inert, visibility: element.style.visibility });
                element.inert = true;
                element.style.visibility = "hidden";
            }
        };
        block();
        const observer = new MutationObserver(block);
        observer.observe(document.body, { childList: true, subtree: true });
        return () => {
            observer.disconnect();
            for (const [element, state] of previous) {
                element.inert = state.inert;
                element.style.visibility = state.visibility;
            }
        };
    }, []);
    return null;
}
