import { getActiveUserScope } from "@/lib/user-scope";
import { captureWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";

export type PlatformDraftInspection = {
    id: string;
    label: string;
    hasUnsynced: boolean;
    reason?: string;
};

export type PlatformDraftPort = {
    id: string;
    scope: string;
    inspect(): PlatformDraftInspection | Promise<PlatformDraftInspection>;
    flush(signal?: AbortSignal): Promise<void>;
};

const draftPorts = new Map<string, PlatformDraftPort>();

export function registerPlatformDraftPort(port: PlatformDraftPort) {
    if (!port.scope.startsWith("platform:") || !port.id) throw new Error("平台草稿必须绑定工作区和对象标识");
    const key = `${port.scope}\0${port.id}`;
    if (draftPorts.has(key)) throw new Error("同一工作区的草稿保存端口已注册");
    draftPorts.set(key, port);
    return () => { if (draftPorts.get(key) === port) draftPorts.delete(key); };
}

export async function inspectRegisteredPlatformDrafts(scope: string) {
    return Promise.all([...draftPorts.values()].filter((port) => port.scope === scope).map((port) => port.inspect()));
}

export async function flushRegisteredPlatformDrafts(scope: string, signal?: AbortSignal) {
    const session = captureWorkspaceSession();
    const assertCurrent = () => {
        if (signal?.aborted || !isWorkspaceSessionCurrent(session) || getActiveUserScope() !== scope) {
            throw new DOMException("工作区会话已变更，草稿保留", "AbortError");
        }
    };
    for (const port of [...draftPorts.values()].filter((entry) => entry.scope === scope)) {
        assertCurrent();
        const inspection = await port.inspect();
        assertCurrent();
        if (inspection.hasUnsynced) await port.flush(signal);
        assertCurrent();
    }
}

export function clearRegisteredPlatformDrafts(scope: string) {
    for (const [key, port] of draftPorts) if (port.scope === scope) draftPorts.delete(key);
}
