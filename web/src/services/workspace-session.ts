import { useUserStore } from "@/stores/use-user-store";

export type WorkspaceSessionIdentity = {
    mode: "local" | "platform";
    accountId?: string;
    workspaceId: string;
    sessionId?: string;
};

export type WorkspaceSessionSnapshot = {
    generation: number;
    identity: WorkspaceSessionIdentity | null;
    reason?: string;
};

export type WorkspaceSessionRequestBinding = {
    signal: AbortSignal;
    generation: number;
    isCurrent(): boolean;
    abort(): void;
    release(): void;
};

let generation = 0;
let identity: WorkspaceSessionIdentity | null = null;
let sessionReason: string | undefined;
const controllers = new Map<AbortController, number>();
const listeners = new Set<(snapshot: WorkspaceSessionSnapshot) => void>();

function snapshot(): WorkspaceSessionSnapshot {
    return { generation, identity: identity ? { ...identity } : null, reason: sessionReason };
}

function notify(nextReason?: string) {
    const current = { ...snapshot(), reason: nextReason };
    for (const listener of listeners) listener(current);
}

function abortOlderRequests() {
    for (const [controller, requestGeneration] of controllers) {
        if (requestGeneration !== generation) controller.abort();
    }
}

export function captureWorkspaceSession(): WorkspaceSessionSnapshot {
    return snapshot();
}

export function isWorkspaceSessionCurrent(value: WorkspaceSessionSnapshot): boolean {
    return value.generation === generation;
}

export function isWorkspaceSessionGenerationCurrent(value: number): boolean {
    return value === generation;
}

type RequestAbortSignal = Pick<AbortSignal, "aborted"> & Partial<Pick<AbortSignal, "addEventListener" | "removeEventListener">>;

export function createWorkspaceSessionRequest(parentSignal?: RequestAbortSignal): WorkspaceSessionRequestBinding {
    const bound = snapshot();
    const controller = new AbortController();
    const abortFromParent = () => controller.abort();
    if (parentSignal?.aborted) controller.abort();
    else parentSignal?.addEventListener?.("abort", abortFromParent, { once: true });
    controllers.set(controller, bound.generation);
    return {
        signal: controller.signal,
        generation: bound.generation,
        isCurrent: () => isWorkspaceSessionCurrent(bound),
        abort: () => controller.abort(),
        release() {
            parentSignal?.removeEventListener?.("abort", abortFromParent);
            controllers.delete(controller);
        },
    };
}

export function activateWorkspaceSession(next: WorkspaceSessionIdentity) {
    if (!next.workspaceId || (next.mode === "platform" && (!next.accountId || !next.sessionId))) {
        throw new Error("工作区会话身份不完整");
    }
    if (
        identity?.mode === next.mode &&
        identity.accountId === next.accountId &&
        identity.workspaceId === next.workspaceId &&
        identity.sessionId === next.sessionId
    ) return snapshot();

    generation += 1;
    identity = { ...next };
    sessionReason = undefined;
    abortOlderRequests();
    notify();
    return snapshot();
}

export function invalidateWorkspaceSession(reason = "session_invalid") {
    if (!identity) return snapshot();
    const wasPlatform = identity.mode === "platform";
    generation += 1;
    identity = null;
    sessionReason = reason;
    abortOlderRequests();
    if (wasPlatform) useUserStore.getState().setAuthState("reauth-required", reason);
    notify(reason);
    return snapshot();
}

export function subscribeWorkspaceSession(listener: (snapshot: WorkspaceSessionSnapshot) => void) {
    listeners.add(listener);
    return () => listeners.delete(listener);
}

export function resetWorkspaceSessionForTests() {
    generation += 1;
    identity = null;
    sessionReason = undefined;
    abortOlderRequests();
    controllers.clear();
    listeners.clear();
}

