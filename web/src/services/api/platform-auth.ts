import { http, setPlatformCsrfToken } from "@/services/api/request";

export type RuntimeModeContract = {
    mode: "local" | "platform";
    contractVersion: number;
    instanceId?: string;
};

export type PlatformAccount = {
    id: string;
    username: string;
    displayName: string;
    workspaceId: string;
    role: "admin" | "user";
    status: "active" | "disabled" | "deleted";
    mustChangePassword: boolean;
    createdAt?: string;
    updatedAt?: string;
};

export type PlatformSessionGrant = {
    account: PlatformAccount;
    sessionId: string;
    csrfToken: string;
    expiresAt: string;
};

export type PlatformWorkspaceBootstrap = {
    contractVersion: number;
    instanceId: string;
    profile: "platform";
    storageMode: "remote";
    account: PlatformAccount;
    sessionId: string;
    workspace: { id: string; name: string; owner: "platform"; storage: "sqlite" };
    capabilities: { localAssets: false; providerCalls: false };
    features: {
        shortDrama: boolean;
        assistant: false;
        plugins: false;
        skills: false;
        generation: false;
    };
};

export async function getRuntimeModeContract() {
    const runtime = await http.get<RuntimeModeContract>("/runtime");
    if (!runtime || !["local", "platform"].includes(runtime.mode) || !Number.isSafeInteger(runtime.contractVersion) || runtime.contractVersion < 1) {
        throw new Error("服务端运行模式合同无效");
    }
    if (runtime.mode === "platform" && !runtime.instanceId?.trim()) throw new Error("平台实例标识缺失");
    return runtime;
}

export async function getPlatformAuthMe(options?: { signal?: AbortSignal; timeout?: number }) {
    const session = await http.get<PlatformSessionGrant>("/auth/me", options);
    options?.signal?.throwIfAborted();
    setPlatformCsrfToken(session.csrfToken);
    return session;
}

export async function loginPlatform(username: string, password: string) {
    const bootstrap = await http.get<{ csrfToken: string }>("/auth/csrf", { params: { purpose: "login" } });
    setPlatformCsrfToken(bootstrap.csrfToken);
    const grant = await http.post<PlatformSessionGrant>("/auth/login", { username, password }, { timeout: 30_000 });
    setPlatformCsrfToken(grant.csrfToken);
    return grant;
}

export async function changePlatformPassword(oldPassword: string, newPassword: string) {
    const grant = await http.post<PlatformSessionGrant>("/auth/password", { oldPassword, newPassword }, { timeout: 30_000 });
    setPlatformCsrfToken(grant.csrfToken);
    return grant;
}

export async function logoutPlatform() {
    const result = await http.post<{ loggedOut: boolean }>("/auth/logout");
    setPlatformCsrfToken(null);
    return result;
}

export function getPlatformWorkspaceBootstrap() {
    return http.get<PlatformWorkspaceBootstrap>("/workspace/bootstrap");
}

export function clearPlatformCsrfToken() {
    setPlatformCsrfToken(null);
}
