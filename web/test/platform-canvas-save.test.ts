import { beforeEach, expect, mock, test } from "bun:test";
import { useUserStore } from "../src/stores/use-user-store";
import { activateWorkspaceSession, resetWorkspaceSessionForTests } from "../src/services/workspace-session";

let scope = "platform:canvas-a";
const storage = new Map<string, string>();
let remote: Record<string, unknown>;
let writes: Array<{ path: string; project: Record<string, unknown> }> = [];
let hold: Promise<void> | undefined;
let started: (() => void) | undefined;
let forcedConflict = false;

mock.module("@/lib/user-scope", () => ({
    getActiveUserScope: () => scope,
    scopedStorageKey: (name: string, user = scope) => `${name}:user:${user}`,
    scopedLocalStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    onStorageNamespaceChange: () => () => {},
}));
mock.module("@/lib/localforage-storage", () => ({
    localForageStorageForScope: (user = scope) => ({
        getItem: async (name: string) => storage.get(`${user}\0${name}`) ?? null,
        setItem: async (name: string, value: string) => { storage.set(`${user}\0${name}`, value); },
        removeItem: async (name: string) => { storage.delete(`${user}\0${name}`); },
    }),
}));
mock.module("@/services/workspace-mode", () => ({ isLocalWorkspaceMode: () => false, getRuntimeMode: () => "platform" }));
mock.module("@/services/local-workspace-sync", () => ({ notifyCanvasRefresh: () => {} }));
mock.module("@/services/canvas-sync-drafts", () => ({ preserveCanvasSyncDraft: async () => 1 }));
mock.module("@/stores/use-asset-store", () => ({ useAssetStore: { getState: () => ({ assets: [] }) } }));
mock.module("@/services/api/resources", () => ({ resourceIdFromStorageKey: () => "" }));
const request = await import("../src/services/api/request");
mock.module("@/services/api/request", () => ({
    ...request,
    http: {
        get: async () => ({ project: structuredClone(remote) }),
        put: async (path: string, body: { project: Record<string, unknown> }) => {
            writes.push({ path, project: structuredClone(body.project) }); started?.();
            await hold;
            if (forcedConflict || body.project.revision !== remote.revision) throw new request.ApiError("版本冲突", { status: 409, reason: "stale_revision" });
            remote = { ...body.project, revision: Number(remote.revision) + 1 };
            return { project: { id: "canvas", revision: remote.revision, updatedAt: remote.updatedAt } };
        },
        post: async () => { throw new Error("平台不允许调用本地 /ops 写入口"); },
    },
}));
useUserStore.setState({ runtimeMode: "platform" });
const { useCanvasStore, withCanvasStorePersistenceSuppressed, recordCanvasDocumentBase, clearCanvasExternalRevisionConflict } = await import("../src/stores/canvas/use-canvas-store");
const { saveCanvasOperationJournal, resetCanvasOperationJournalMemory } = await import("../src/services/canvas-operation-journal");
const { persistCanvasDocument, hasUnconfirmedCanvasEdits, resetLocalCanvasBackendSaveState } = await import("../src/services/local-workspace-repository");
const { resumeCanvasBackendSubmit } = await import("../src/services/canvas-revision-conflict");

function canvas(title = "原名称") {
    return { id: "canvas", title, revision: 1, createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", nodes: [], connections: [], chatSessions: [], activeChatId: null, backgroundMode: "grid", showImageInfo: false, viewport: { x: 0, y: 0, k: 1 } } as never;
}

beforeEach(async () => {
    resetWorkspaceSessionForTests(); scope = "platform:canvas-a";
    activateWorkspaceSession({ mode: "platform", accountId: "a", workspaceId: "canvas-a", sessionId: "a-session" });
    storage.clear(); writes = []; hold = undefined; started = undefined; forcedConflict = false;
    resetLocalCanvasBackendSaveState(); resetCanvasOperationJournalMemory(); resumeCanvasBackendSubmit("canvas", scope);
    clearCanvasExternalRevisionConflict(scope, "canvas");
    const base = canvas(); remote = structuredClone(base);
    await saveCanvasOperationJournal({ userScope: scope, canvasId: "canvas", confirmedRevision: 1, confirmedSnapshot: base, inFlight: null });
    recordCanvasDocumentBase(base, scope);
    withCanvasStorePersistenceSuppressed(() => useCanvasStore.setState({ projects: [base], hydrated: true }));
});

test("平台画布修改发送真实PUT并确认新revision，不调用本地ops", async () => {
    await persistCanvasDocument("canvas", { title: "保存后的名称" });
    expect(writes).toHaveLength(1);
    expect(writes[0].path).toBe("/canvas-projects/canvas");
    expect(writes[0].project.revision).toBe(1);
    expect(remote.title).toBe("保存后的名称");
    expect(useCanvasStore.getState().openProject("canvas")?.revision).toBe(2);
    expect(hasUnconfirmedCanvasEdits("canvas")).toBe(false);
});

test("平台409同字段冲突保留草稿并报告失败", async () => {
    remote = { ...remote, title: "另一个设备", revision: 2 }; forcedConflict = true;
    await expect(persistCanvasDocument("canvas", { title: "本机草稿" })).rejects.toThrow();
    expect(useCanvasStore.getState().openProject("canvas")?.title).toBe("本机草稿");
    expect(remote.title).toBe("另一个设备");
    expect(hasUnconfirmedCanvasEdits("canvas")).toBe(true);
});

test("平台409不同字段变化复用三方合并并按新revision重试", async () => {
    remote = { ...remote, title: "另一个设备改名", revision: 2 };
    const node = { id: "new-node", type: "text", title: "本机新增节点", position: { x: 0, y: 0 }, width: 300, height: 200, metadata: {} };
    await persistCanvasDocument("canvas", { nodes: [node as never] });
    expect(writes.map((write) => write.project.revision)).toEqual([1, 2]);
    expect(remote.title).toBe("另一个设备改名");
    expect((remote.nodes as Array<{ id: string }>).map((entry) => entry.id)).toEqual(["new-node"]);
    expect(hasUnconfirmedCanvasEdits("canvas")).toBe(false);
});

test("A画布保存回执晚到不能更新B的同名画布", async () => {
    let release!: () => void; let notify!: () => void;
    hold = new Promise<void>((resolve) => { release = resolve; });
    const dispatched = new Promise<void>((resolve) => { notify = resolve; }); started = notify;
    const saving = persistCanvasDocument("canvas", { title: "A修改" }).catch((error) => error);
    await dispatched;
    scope = "platform:canvas-b";
    activateWorkspaceSession({ mode: "platform", accountId: "b", workspaceId: "canvas-b", sessionId: "b-session" });
    withCanvasStorePersistenceSuppressed(() => useCanvasStore.setState({ projects: [canvas("B的画布")] }));
    release();
    expect(await saving).toMatchObject({ name: "AbortError" });
    expect(useCanvasStore.getState().openProject("canvas")?.title).toBe("B的画布");
    expect(useCanvasStore.getState().openProject("canvas")?.revision).toBe(1);
});
