import { rebindInconsistentCanvasAssets, type CanvasAssetRebindResult } from "@/services/canvas-asset-repair";
import { createLocalCanvasProject, deleteLocalCanvasProjects, flushLocalCanvasBackendSaves, hasUnconfirmedCanvasEdits, openLocalCanvasProject, openLocalCanvasProjectFromBackend, persistCanvasDocument, restoreLocalCanvasProjectFromHistory, syncLocalCanvasProjectToBackend } from "@/services/local-workspace-repository";
import { getRuntimeMode } from "@/services/workspace-mode";
import { captureWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";
import { flushAssetStorePersistence, useAssetStore, type Asset } from "@/stores/use-asset-store";
import {
    loadWorkspaceAssetLibraryPage,
    loadWorkspaceAssetsForUse,
    type WorkspaceAssetLibraryPageOptions,
} from "@/services/workspace-asset-read";
import { flushCanvasStorePersistence, useCanvasStore, type CanvasProject } from "@/stores/canvas/use-canvas-store";

export { isLocalWorkspaceMode } from "@/services/workspace-mode";

let operationTail: Promise<void> = Promise.resolve();
const canvasRefreshListeners = new Set<(project: CanvasProject, previous: CanvasProject | undefined) => void>();

/** 保留已有调用入口，平台同步复用画布仓储中的版本校验与合并。 */
export function hasRemoteUserDataSyncSession() {
    return getRuntimeMode() === "platform";
}

export function withRemoteUserDataSyncExclusive<T>(operation: () => Promise<T>): Promise<T> {
    const next = operationTail.then(operation, operation);
    operationTail = next.then(() => undefined, () => undefined);
    return next;
}

export async function loadCanvasProjectForEditing(
    id: string,
    options: { latest?: boolean; historyRestore?: { snapshotId: string; revision: number }; onLoad?: (project: CanvasProject) => void } = {},
) {
    if (options.historyRestore) {
        const restored = await restoreLocalCanvasProjectFromHistory(id, options.historyRestore);
        options.onLoad?.(restored);
        return restored;
    }
    const project = await openLocalCanvasProjectFromBackend(id);
    if (project) options.onLoad?.(project);
    return project || undefined;
}

// 画布内容被其他写入者（远端刷新、任务回写）替换后通知本地编辑器合并。
export function subscribeCanvasRefresh(listener: (project: CanvasProject, previous: CanvasProject | undefined) => void) {
    canvasRefreshListeners.add(listener);
    return () => { canvasRefreshListeners.delete(listener); };
}

/** 外部写入已投影到本地存储后，把这次替换交给正在编辑的页面。 */
export function notifyCanvasRefresh(project: CanvasProject, previous: CanvasProject | undefined) {
    for (const listener of [...canvasRefreshListeners]) listener(project, previous);
}

/**
 * Persist an editor snapshot to the co-packaged Go repository. Callers that
 * mutate the canvas outside the autosave flow (for example node deletion) must
 * use this bridge so the next remote refresh cannot resurrect a stale snapshot.
 */
export async function syncLocalCanvasSnapshot(id: string, patch: Partial<Pick<CanvasProject, "nodes" | "connections" | "chatSessions" | "activeChatId" | "appearance" | "backgroundMode" | "showImageInfo" | "viewport">>) {
    const current = openLocalCanvasProject(id);
    if (!current) throw new Error("本地画布不存在");
    const { viewport, ...documentPatch } = patch;
    if (viewport) useCanvasStore.getState().updateProject(id, { viewport });
    if (Object.keys(documentPatch).length > 0) await persistCanvasDocument(id, documentPatch);
    const saved = openLocalCanvasProject(id);
    if (!saved) throw new Error("本地画布不存在");
    return saved;
}

export type LocalAssetPageOptions = WorkspaceAssetLibraryPageOptions;

export function loadAssetLibraryPage(options: LocalAssetPageOptions) {
    return loadWorkspaceAssetLibraryPage(options);
}

export function loadAssetsForUse(ids: Iterable<string>) {
    return loadWorkspaceAssetsForUse(ids);
}

export function localSavedRemotePendingMessage(localAction: string, error: unknown) {
    const detail = error instanceof Error && error.message.trim() ? error.message.trim() : "未知错误";
    return `${localAction}失败：${detail}`;
}

export async function createCanvasProjectWithRemoteSync(
    title: string,
    projectId?: string,
    initialContent?: Partial<Pick<CanvasProject, "nodes" | "connections" | "chatSessions" | "activeChatId">>,
): Promise<{ id: string; syncError?: unknown }> {
    return { ...await createLocalCanvasProject(title, projectId, initialContent), syncError: undefined };
}

export async function deleteAssetWithRemoteSync(id: string) {
    const assetId = id.trim();
    if (!assetId) throw new Error("素材 ID 不能为空");
    await useAssetStore.getState().removeAsset(assetId);
    await flushAssetStorePersistence();
}

export function deleteCanvasProjectsWithRemoteSync(ids: string[]) {
    return deleteLocalCanvasProjects(ids);
}

/** Legacy save name retained while callers migrate to workspace repositories. */
export async function saveRemoteUserDataNow(_input?: string | readonly string[] | { force?: boolean }) {
    const session = captureWorkspaceSession();
    const assertCurrent = () => { if (!isWorkspaceSessionCurrent(session)) throw new DOMException("工作区会话已变更", "AbortError"); };
    await Promise.all([flushCanvasStorePersistence(), flushAssetStorePersistence()]);
    assertCurrent();
    if (getRuntimeMode() !== "platform") return;
    const ids = typeof _input === "string" ? [_input] : Array.isArray(_input) ? _input : useCanvasStore.getState().projects.map((project) => project.id);
    for (const id of ids) {
        assertCurrent();
        if (hasUnconfirmedCanvasEdits(id)) await syncLocalCanvasProjectToBackend(id);
    }
    await flushLocalCanvasBackendSaves();
}

export function scheduleRemoteUserDataSync() {
    void saveRemoteUserDataNow().catch((error) => console.error("本地工作区保存失败", error));
}

export async function forceOverwriteRemoteCanvasSync(): Promise<CanvasAssetRebindResult> {
    const result = rebindInconsistentCanvasAssets(useAssetStore.getState().assets);
    await saveRemoteUserDataNow();
    return result;
}
