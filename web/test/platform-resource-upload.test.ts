import { afterEach, beforeEach, expect, test } from "bun:test";
import { apiClient } from "../src/services/api/request";
import { uploadResourceFile } from "../src/services/api/resources";
import { getRuntimeMode, setRuntimeMode } from "../src/services/workspace-mode";
import { activateWorkspaceSession, resetWorkspaceSessionForTests } from "../src/services/workspace-session";
import { getActiveUserScope, setActiveUserScope } from "../src/lib/user-scope";

const adapter = apiClient.defaults.adapter;
const mode = getRuntimeMode();
const scope = getActiveUserScope();
beforeEach(() => {
    setRuntimeMode("platform");
    setActiveUserScope("platform:alice");
    activateWorkspaceSession({ mode: "platform", accountId: "alice", workspaceId: "wa", sessionId: "sa" });
});
afterEach(() => {
    apiClient.defaults.adapter = adapter;
    resetWorkspaceSessionForTests();
    setRuntimeMode(mode);
    setActiveUserScope(scope);
});

test("平台小文件也先申请持久分片预留，再收数据和完成", async () => {
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(`${config.method} ${config.url}`);
        expect(config.headers.get("X-Platform-Session")).toBe("sa");
        let data: unknown = {};
        if (config.url === "/resources/uploads") {
            expect(config.headers.get("X-Idempotency-Key")).toBe("same-operation");
            expect(JSON.parse(config.data).contentDigest).toMatch(/^sha256-chunks-v1:[a-f0-9]{64}$/);
            data = { uploadId: "upload", chunkSize: 8 << 20, chunkCount: 1, received: [] };
        }
        if (config.url?.endsWith("/complete")) data = { resource: { id: "resource", userId: "wa", status: "ready", size: 4 } };
        return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, data, msg: "ok" } };
    };
    const result = await uploadResourceFile(new Blob(["test"]), "file", { idempotencyKey: "same-operation" });
    expect(result.id).toBe("resource");
    expect(calls).toEqual(["post /resources/uploads", "put /resources/uploads/upload/chunks/0", "post /resources/uploads/upload/complete"]);
});

test("上传会话回复前切换账号，不能发送分片或完成请求", async () => {
    const calls: string[] = [];
    apiClient.defaults.adapter = async (config) => {
        calls.push(config.url ?? "");
        activateWorkspaceSession({ mode: "platform", accountId: "bob", workspaceId: "wb", sessionId: "sb" });
        setActiveUserScope("platform:bob");
        return { config, status: 200, statusText: "OK", headers: {}, data: { code: 0, data: { uploadId: "upload", chunkSize: 8 << 20, chunkCount: 1, received: [] }, msg: "ok" } };
    };
    await expect(uploadResourceFile(new Blob(["test"]), "file")).rejects.toMatchObject({ name: "AbortError" });
    expect(calls).toEqual(["/resources/uploads"]);
});
