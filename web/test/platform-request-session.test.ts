import { afterEach, expect, test } from "bun:test";
import { AxiosError } from "axios";
import { apiClient, http, setPlatformCsrfToken } from "../src/services/api/request";
import { activateWorkspaceSession, captureWorkspaceSession, invalidateWorkspaceSession, isWorkspaceSessionCurrent, resetWorkspaceSessionForTests } from "../src/services/workspace-session";
import { resetWorkspaceMode, setRuntimeMode } from "../src/services/workspace-mode";
import { useUserStore } from "../src/stores/use-user-store";
import { platformWorkspaceStorageScope } from "../src/lib/user-scope";

const originalAdapter = apiClient.defaults.adapter;
const alice = { mode: "platform" as const, accountId: "a", workspaceId: "wa", sessionId: "sa" };
const bob = { mode: "platform" as const, accountId: "b", workspaceId: "wb", sessionId: "sb" };

afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
    setPlatformCsrfToken(null);
    resetWorkspaceSessionForTests();
    resetWorkspaceMode();
    useUserStore.getState().clearSession();
});

test("写请求携带原会话及CSRF，切换后旧响应不能返回给业务", async () => {
    setRuntimeMode("platform");
    activateWorkspaceSession(alice);
    setPlatformCsrfToken("csrf-a");
    let complete!: () => void;
    let started!: () => void;
    const ready = new Promise<void>((resolve) => { started = resolve; });
    apiClient.defaults.adapter = (config) => new Promise((resolve) => {
        expect(config.headers.get("X-Platform-Session")).toBe("sa");
        expect(config.headers.get("X-CSRF-Token")).toBe("csrf-a");
        complete = () => resolve({ config, data: { code: 0, data: { secret: "a-only" }, msg: "ok" }, status: 200, statusText: "OK", headers: {} });
        started();
    });
    const pending = http.post("/projects", { name: "项目" }).catch((error) => error);
    await ready;
    activateWorkspaceSession(bob);
    complete();
    expect(await pending).toMatchObject({ name: "AbortError" });
});

test("旧会话迟到的401不能使新账号失效", async () => {
    setRuntimeMode("platform");
    activateWorkspaceSession(alice);
    let complete!: () => void;
    let started!: () => void;
    const ready = new Promise<void>((resolve) => { started = resolve; });
    apiClient.defaults.adapter = (config) => new Promise((_resolve, reject) => {
        complete = () => reject(new AxiosError("会话已失效", "ERR_BAD_REQUEST", config, undefined, {
            config, status: 401, statusText: "Unauthorized", headers: {}, data: { reason: "unauthorized" },
        }));
        started();
    });
    const pending = http.get("/projects").catch((error) => error);
    await ready;
    activateWorkspaceSession(bob);
    complete();
    expect(await pending).toMatchObject({ name: "AbortError" });
    expect(captureWorkspaceSession().identity).toEqual(bob);
});

test("无会话时平台业务请求停止，但认证入口可用", async () => {
    setRuntimeMode("platform");
    let calls = 0;
    apiClient.defaults.adapter = async (config) => {
        calls += 1;
        return { config, data: { code: 0, data: {}, msg: "ok" }, status: 200, statusText: "OK", headers: {} };
    };
    await expect(http.get("/projects")).rejects.toMatchObject({ name: "AbortError" });
    await http.get("/auth/me");
    expect(calls).toBe(1);
});

test("撤销取消所有旧请求并保留用户草稿恢复身份", () => {
    setRuntimeMode("platform");
    useUserStore.getState().setUser({ id: "a", workspaceId: "wa", username: "alice", displayName: "A", role: "user", status: "active" });
    activateWorkspaceSession(alice);
    const old = captureWorkspaceSession();
    invalidateWorkspaceSession("session_revoked");
    expect(isWorkspaceSessionCurrent(old)).toBe(false);
    expect(useUserStore.getState().authState).toBe("reauth-required");
    expect(useUserStore.getState().user?.id).toBe("a");
});

test("请求调用后立即切换会话也不能使用新账号身份发送", async () => {
    setRuntimeMode("platform");
    activateWorkspaceSession(alice);
    let sentSession: unknown;
    apiClient.defaults.adapter = async (config) => {
        sentSession = config.headers.get("X-Platform-Session");
        return { config, data: { code: 0, data: {}, msg: "ok" }, status: 200, statusText: "OK", headers: {} };
    };
    const pending = http.post("/projects", { name: "甲的项目" }).catch((error) => error);
    activateWorkspaceSession(bob);
    expect(await pending).toMatchObject({ name: "AbortError" });
    expect(sentSession).toBe("sa");
});

test("已验证身份后的旧密码错误不撤销当前工作区", async () => {
    setRuntimeMode("platform");
    activateWorkspaceSession(alice);
    apiClient.defaults.adapter = async (config) => {
        throw new AxiosError("旧密码错误", "ERR_BAD_REQUEST", config, undefined, {
            config, status: 401, statusText: "Unauthorized", headers: { "x-platform-session": "sa" }, data: { reason: "unauthorized" },
        });
    };
    await expect(http.post("/auth/password", { oldPassword: "wrong" })).rejects.toMatchObject({ reason: "unauthorized" });
    expect(captureWorkspaceSession().identity).toEqual(alice);
});

test("同一工作区在不同平台实例使用独立缓存命名空间", () => {
    setRuntimeMode("platform", "instance-a");
    const first = platformWorkspaceStorageScope("workspace-a");
    setRuntimeMode("platform", "instance-b");
    expect(platformWorkspaceStorageScope("workspace-a")).not.toBe(first);
    setRuntimeMode("platform", "instance-a");
    expect(platformWorkspaceStorageScope("workspace-a")).toBe(first);
    resetWorkspaceMode();
    expect(() => platformWorkspaceStorageScope("workspace-a")).toThrow("平台实例或工作区标识缺失");
});
