import { MEDIA_FILES_STORE_NAME, localForageInstance, localForageKeyBelongsToScope } from "@/lib/localforage-storage";
import { getActiveUserScope } from "@/lib/user-scope";
import { captureWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";

const store = localForageInstance(MEDIA_FILES_STORE_NAME);
const objectUrls = new Map<string, string>();

/** Local-only media persistence. This module never calls a network or resource API. */
export async function saveLocalMedia(storageKey: string, blob: Blob, previewUrl?: string) {
    const session = captureWorkspaceSession();
    await store.setItem(storageKey, blob);
    if (!isWorkspaceSessionCurrent(session)) throw new DOMException("工作区会话已变更", "AbortError");
    const url = previewUrl || URL.createObjectURL(blob);
    objectUrls.set(storageKey, url);
    return url;
}

export async function resolveLocalMediaUrl(storageKey: string, fallback = "") {
    const session = captureWorkspaceSession();
    const cached = objectUrls.get(storageKey);
    if (cached) return cached;
    const blob = await store.getItem<Blob>(storageKey);
    if (!isWorkspaceSessionCurrent(session)) throw new DOMException("工作区会话已变更", "AbortError");
    if (!blob) return fallback;
    const url = URL.createObjectURL(blob);
    objectUrls.set(storageKey, url);
    return url;
}

export function getLocalMediaBlob(storageKey: string) {
    return store.getItem<Blob>(storageKey);
}

export async function setLocalMediaBlob(storageKey: string, blob: Blob) {
    return saveLocalMedia(storageKey, blob);
}

export async function deleteLocalMedia(keys: Iterable<string>) {
    await Promise.all(
        Array.from(new Set(keys)).map(async (key) => {
            const url = objectUrls.get(key);
            if (url) URL.revokeObjectURL(url);
            objectUrls.delete(key);
            await store.removeItem(key);
        }),
    );
}

export async function cleanupLocalMedia(usedKeys: Set<string>, scope = getActiveUserScope()) {
    const unused: string[] = [];
    await store.iterate((_value, key) => {
        if (localForageKeyBelongsToScope(key, scope, MEDIA_FILES_STORE_NAME) && !usedKeys.has(key)) unused.push(key);
    });
    await Promise.all(unused.map(async (key) => {
        const url = objectUrls.get(key);
        if (url) URL.revokeObjectURL(url);
        objectUrls.delete(key);
        await store.removeItem(key);
    }));
}

export function clearLocalMediaObjectUrlsForScope(scope: string) {
    for (const [key, url] of objectUrls) {
        if (!localForageKeyBelongsToScope(key, scope, MEDIA_FILES_STORE_NAME)) continue;
        URL.revokeObjectURL(url);
        objectUrls.delete(key);
    }
}
