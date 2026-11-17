import {
    APP_STATE_STORE_NAME,
    CANVAS_FOLDER_PENDING_STORE_NAME,
    DRAWING_DOCUMENTS_STORE_NAME,
    deleteLocalForageScopeData,
    localForageInstance,
    localForageKeyBelongsToScope,
    lockLocalForageScope,
    unlockLocalForageScope,
} from "@/lib/localforage-storage";
import { appQueryClient } from "@/lib/query-client";
import { getActiveUserScope } from "@/lib/user-scope";
import { resetPersistedStoreMemory } from "@/lib/reset-persisted-store-memory";
import {
    clearRegisteredPlatformDrafts,
    flushRegisteredPlatformDrafts,
    inspectRegisteredPlatformDrafts,
    type PlatformDraftInspection,
} from "@/services/platform-workspace-drafts";
import { captureWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";

export { registerPlatformDraftPort } from "@/services/platform-workspace-drafts";
export type { PlatformDraftInspection, PlatformDraftPort } from "@/services/platform-workspace-drafts";

function requirePlatformScope(scope: string) {
    if (!scope.startsWith("platform:") || scope.length === "platform:".length) throw new Error("平台工作区存储范围缺失");
    return scope;
}

export function activatePlatformWorkspaceCacheScope(scope: string) {
    unlockLocalForageScope(requirePlatformScope(scope));
}

export type PlatformDraftReport = { hasUnsynced: boolean; drafts: PlatformDraftInspection[] };

async function inspectPersistedDrafts(scope: string): Promise<PlatformDraftInspection[]> {
    const drafts: PlatformDraftInspection[] = [];
    const add = (id: string, label: string, reason?: string) => drafts.push({ id, label, reason, hasUnsynced: true });
    await localForageInstance(APP_STATE_STORE_NAME).iterate<unknown, void>((raw, key) => {
        if (!localForageKeyBelongsToScope(key, scope, APP_STATE_STORE_NAME)) return;
        if (key.startsWith("infinite-canvas:sync-drafts:") || key.startsWith("director-scene-draft:")) {
            try {
                const value = typeof raw === "string" ? JSON.parse(raw) : raw;
                if (Array.isArray(value) ? value.length > 0 : Boolean(value)) add(key, key.startsWith("director-") ? "导演场景草稿" : "画布冲突草稿", "仍有保留在本机的草稿");
            } catch { add(key, "无法读取的草稿", "草稿数据无法读取，请先恢复或明确放弃"); }
        }
    });
    await localForageInstance(DRAWING_DOCUMENTS_STORE_NAME).iterate<{ draft?: unknown }, void>((value, key) => {
        if (localForageKeyBelongsToScope(key, scope, DRAWING_DOCUMENTS_STORE_NAME) && value?.draft) add(key, "画板草稿");
    });
    const folders = await localForageInstance(CANVAS_FOLDER_PENDING_STORE_NAME).getItem<{ intents?: Record<string, unknown> }>(scope);
    for (const id of Object.keys(folders?.intents || {})) add(`folder:${id}`, "画布目录修改");
    return drafts;
}

export async function inspectPlatformDrafts(scope = getActiveUserScope()): Promise<PlatformDraftReport> {
    requirePlatformScope(scope);
    const session = captureWorkspaceSession();
    const assertCurrent = () => {
        if (!isWorkspaceSessionCurrent(session)) throw new DOMException("工作区会话已变更", "AbortError");
    };
    const drafts = [...await inspectRegisteredPlatformDrafts(scope), ...await inspectPersistedDrafts(scope)];
    assertCurrent();
    if (scope === getActiveUserScope()) {
        const [{ useCanvasStore, pendingCanvasStorePersistence }, { hasUnconfirmedCanvasEdits }, assets] = await Promise.all([
            import("@/stores/canvas/use-canvas-store"),
            import("@/services/local-workspace-repository"),
            import("@/stores/use-asset-store"),
        ]);
        assertCurrent();
        for (const canvas of useCanvasStore.getState().projects) {
            if (hasUnconfirmedCanvasEdits(canvas.id)) drafts.push({ id: `canvas:${canvas.id}`, label: canvas.title || "画布", hasUnsynced: true });
        }
        if (pendingCanvasStorePersistence(scope)) drafts.push({ id: "canvas-cache", label: "画布本地写入", hasUnsynced: true });
        await assets.hydrateAssetStoreDrafts(scope);
        assertCurrent();
        const pending = assets.readAssetStoreDrafts();
        for (const asset of [...pending.upserts, ...pending.deletes]) drafts.push({ id: `asset:${asset.id}`, label: "素材修改", hasUnsynced: true });
    }
    return { hasUnsynced: drafts.some((draft) => draft.hasUnsynced), drafts: drafts.filter((draft) => draft.hasUnsynced) };
}

export async function flushPlatformDrafts(options: { scope?: string; signal?: AbortSignal } = {}) {
    const scope = requirePlatformScope(options.scope ?? getActiveUserScope());
    const session = captureWorkspaceSession();
    const assertCurrent = () => {
        if (options.signal?.aborted || !isWorkspaceSessionCurrent(session) || scope !== getActiveUserScope()) throw new DOMException("工作区会话已变更，草稿保留", "AbortError");
    };
    assertCurrent();
    await flushRegisteredPlatformDrafts(scope, options.signal);
    assertCurrent();
    const [sync, assets, folders] = await Promise.all([import("@/services/local-workspace-sync"), import("@/services/workspace-asset-repository"), import("@/lib/canvas/canvas-folder-storage")]);
    assertCurrent();
    await sync.saveRemoteUserDataNow();
    assertCurrent();
    await assets.persistWorkspaceAssetChanges();
    assertCurrent();
    await folders.flushCanvasFolderDrafts();
    assertCurrent();
    const remaining = await inspectPlatformDrafts(scope);
    assertCurrent();
    if (remaining.hasUnsynced) throw new Error(`仍有 ${remaining.drafts.length} 项修改未同步，请完成同步或明确放弃后退出`);
}

function clearBrowserStorageScope(storage: Storage, scope: string) {
    const keys: string[] = [];
    for (let index = 0; index < storage.length; index += 1) {
        const key = storage.key(index);
        if (key?.endsWith(`:user:${scope}`)) keys.push(key);
    }
    for (const key of keys) storage.removeItem(key);
}

/** 调用方只在用户确认退出或放弃草稿后调用；任一清理失败都阻止切换账号。 */
export async function clearPlatformWorkspaceCache(scope: string) {
    requirePlatformScope(scope);
    lockLocalForageScope(scope);
    await appQueryClient.cancelQueries();
    const [canvas, assets, repository, journal, blobs, images, media, previews, resources] = await Promise.all([
        import("@/stores/canvas/use-canvas-store"), import("@/stores/use-asset-store"),
        import("@/services/local-workspace-repository"), import("@/services/canvas-operation-journal"),
        import("@/services/resource-blob-cache"), import("@/services/image-storage"),
        import("@/services/local-media-repository"), import("@/services/canvas-video-preview"),
        import("@/services/api/resources"),
    ]);
    await repository.clearCanvasBackendStateForScope(scope);
    await Promise.all([
        canvas.clearCanvasStoreMemoryForScope(scope), assets.clearAssetStoreMemoryForScope(scope),
        journal.clearCanvasOperationJournalMemoryForScope(scope), blobs.clearResourceBlobCacheForScope(scope),
    ]);
    images.clearImageObjectUrlsForScope(scope);
    media.clearLocalMediaObjectUrlsForScope(scope);
    previews.clearCanvasVideoPreviewsForScope(scope);
    resources.clearResourceMetadataCacheForScope(scope);
    if (scope === getActiveUserScope()) {
        const [history, config, plugins, preferences, director, ui, progress] = await Promise.all([
            import("@/stores/canvas/use-canvas-history-store"), import("@/stores/use-config-store"),
            import("@/stores/use-plugin-store"), import("@/stores/use-creation-preferences-store"),
            import("@/stores/canvas/use-director-workbench-store"), import("@/stores/canvas/use-canvas-ui-store"),
            import("@/stores/use-sync-progress-store"),
        ]);
        resetPersistedStoreMemory(history.useCanvasHistoryStore, { deletedProjects: [] });
        resetPersistedStoreMemory(config.useConfigStore, { config: { ...config.defaultConfig, channels: [] } });
        resetPersistedStoreMemory(plugins.usePluginStore, { installations: [], runtimeStatuses: {}, pluginStates: {}, hydrated: false });
        resetPersistedStoreMemory(preferences.useCreationPreferencesStore, { preferences: {}, hydrated: false });
        director.useDirectorWorkbenchStore.getState().reset();
        ui.useCanvasUiStore.setState({ editingProjectId: null, editingProjectTitle: "", selectedProjectIds: [], deleteProjectIds: [] });
        progress.useSyncProgressStore.setState((state) => ({ syncingProjects: Object.fromEntries(Object.entries(state.syncingProjects).filter(([key]) => !key.startsWith(`${scope}\0`))) }));
    }
    await deleteLocalForageScopeData(scope);
    if (typeof window !== "undefined") {
        clearBrowserStorageScope(window.localStorage, scope);
        clearBrowserStorageScope(window.sessionStorage, scope);
    }
    clearRegisteredPlatformDrafts(scope);
    appQueryClient.clear();
}
