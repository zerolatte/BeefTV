import { afterEach, beforeEach, expect, test } from "bun:test";
import type LocalForage from "localforage";
import {
    APP_STATE_STORE_NAME, CANVAS_FOLDER_PENDING_STORE_NAME, IMAGE_FILES_STORE_NAME,
    INFINITE_CANVAS_OBJECT_STORES, MEDIA_FILES_STORE_NAME, RESOURCE_BLOBS_STORE_NAME,
    deleteLocalForageScopeData, installLocalForageStoreFactoryForTests,
    localForageInstance, localForageScopeIsLocked, unlockLocalForageScope,
} from "../src/lib/localforage-storage";
import { setActiveUserScope } from "../src/lib/user-scope";
import { activateWorkspaceSession, resetWorkspaceSessionForTests } from "../src/services/workspace-session";
import { clearRegisteredPlatformDrafts, flushRegisteredPlatformDrafts, inspectRegisteredPlatformDrafts, registerPlatformDraftPort } from "../src/services/platform-workspace-drafts";
import { apiClient } from "../src/services/api/request";
import { getResource, clearResourceMetadataCacheForScope } from "../src/services/api/resources";
import { cacheResourceObjectUrl, clearResourceBlobCacheForScope, peekCachedResourceObjectUrl } from "../src/services/resource-blob-cache";

const data = new Map<string, Map<string, unknown>>();
let beforeWrite: (() => Promise<void>) | undefined;
let beforeRead: (() => Promise<void>) | undefined;
let failRemove = false;
const a = "platform:cache-a";
const b = "platform:cache-ab";
const originalAdapter = apiClient.defaults.adapter;

function deferred() {
    let resolve!: () => void;
    const promise = new Promise<void>((done) => { resolve = done; });
    return { promise, resolve };
}

function activate(scope: string) {
    setActiveUserScope(scope);
    activateWorkspaceSession({ mode: "platform", accountId: scope, workspaceId: scope.slice(9), sessionId: `${scope}:session` });
}

beforeEach(() => {
    data.clear(); beforeWrite = undefined; beforeRead = undefined; failRemove = false;
    unlockLocalForageScope(a); unlockLocalForageScope(b);
    resetWorkspaceSessionForTests(); activate(a);
    installLocalForageStoreFactoryForTests((name) => {
        const values = new Map<string, unknown>(); data.set(name, values);
        return {
            ready: async () => undefined,
            getItem: async (key: string) => { await beforeRead?.(); return values.get(key) ?? null; },
            setItem: async (key: string, value: unknown) => { await beforeWrite?.(); values.set(key, value); return value; },
            removeItem: async (key: string) => { if (failRemove) throw new Error("删除失败"); values.delete(key); },
            keys: async () => [...values.keys()], length: async () => values.size,
            iterate: async (callback: (value: unknown, key: string, index: number) => unknown) => {
                let index = 0; for (const [key, value] of values) callback(value, key, ++index);
            },
        } as unknown as LocalForage;
    });
});

afterEach(async () => {
    apiClient.defaults.adapter = originalAdapter;
    await Promise.all([clearResourceBlobCacheForScope(a), clearResourceBlobCacheForScope(b)]);
    clearResourceMetadataCacheForScope(a); clearResourceMetadataCacheForScope(b);
    clearRegisteredPlatformDrafts(a); clearRegisteredPlatformDrafts(b);
    unlockLocalForageScope(a); unlockLocalForageScope(b);
    installLocalForageStoreFactoryForTests(); resetWorkspaceSessionForTests(); setActiveUserScope("guest");
});

function scopedKey(store: string, scope: string) {
    if (store === APP_STATE_STORE_NAME) return `document:user:${scope}`;
    if (store === CANVAS_FOLDER_PENDING_STORE_NAME) return scope;
    if (store === IMAGE_FILES_STORE_NAME || store === MEDIA_FILES_STORE_NAME) return `image:${scope}:resource`;
    return `${scope}:resource:file`;
}

test("清理精确删除九类工作区数据，保留前缀相近账号与local数据", async () => {
    for (const name of INFINITE_CANVAS_OBJECT_STORES) {
        const store = localForageInstance(name);
        for (const scope of [a, b, "local"]) await store.setItem(scopedKey(name, scope), scope);
    }
    await deleteLocalForageScopeData(a);
    for (const name of INFINITE_CANVAS_OBJECT_STORES) {
        expect(data.get(name)?.has(scopedKey(name, a))).toBe(false);
        expect(data.get(name)?.get(scopedKey(name, b))).toBe(b);
        expect(data.get(name)?.get(scopedKey(name, "local"))).toBe("local");
    }
    expect(localForageScopeIsLocked(a)).toBe(true);
});

test("清理等待已开始的写入，锁后迟到写入拒绝；重新认证解锁后可写", async () => {
    const gate = deferred(); const started = deferred();
    beforeWrite = async () => { started.resolve(); await gate.promise; };
    const store = localForageInstance(RESOURCE_BLOBS_STORE_NAME);
    const key = scopedKey(RESOURCE_BLOBS_STORE_NAME, a);
    const writing = store.setItem(key, "旧数据");
    await started.promise;
    let cleared = false;
    const cleaning = deleteLocalForageScopeData(a).then(() => { cleared = true; });
    await Promise.resolve(); expect(cleared).toBe(false);
    await expect(store.setItem(key, "迟到数据")).rejects.toThrow("已退出");
    gate.resolve(); await writing; await cleaning;
    expect(data.get(RESOURCE_BLOBS_STORE_NAME)?.has(key)).toBe(false);
    beforeWrite = undefined; unlockLocalForageScope(a);
    await store.setItem(key, "新会话");
    expect(await store.getItem(key)).toBe("新会话");
});

test("旧会话读操作迟到时不能返回旧账号数据", async () => {
    const gate = deferred(); const started = deferred();
    const store = localForageInstance(APP_STATE_STORE_NAME);
    await store.setItem(scopedKey(APP_STATE_STORE_NAME, a), "A草稿");
    beforeRead = async () => { started.resolve(); await gate.promise; };
    const reading = store.getItem(scopedKey(APP_STATE_STORE_NAME, a));
    const rejected = reading.catch((error) => error);
    await started.promise; activate(b); gate.resolve();
    expect(await rejected).toMatchObject({ name: "AbortError" });
});

test("任一持久删除失败时抛错并保持scope锁", async () => {
    await localForageInstance(APP_STATE_STORE_NAME).setItem(scopedKey(APP_STATE_STORE_NAME, a), "草稿");
    failRemove = true;
    await expect(deleteLocalForageScopeData(a)).rejects.toThrow("清理失败");
    expect(localForageScopeIsLocked(a)).toBe(true);
});

test("草稿同步失败保留注册内容且不触碰另一个工作区", async () => {
    let aDirty = true; let bFlushed = false; let shouldFail = true;
    registerPlatformDraftPort({ id: "project", scope: a, inspect: () => ({ id: "project", label: "项目", hasUnsynced: aDirty }), flush: async () => { if (shouldFail) throw new Error("版本冲突"); aDirty = false; } });
    registerPlatformDraftPort({ id: "project", scope: b, inspect: () => ({ id: "project", label: "项目", hasUnsynced: true }), flush: async () => { bFlushed = true; } });
    await expect(flushRegisteredPlatformDrafts(a)).rejects.toThrow("版本冲突");
    expect((await inspectRegisteredPlatformDrafts(a))[0].hasUnsynced).toBe(true);
    shouldFail = false; await flushRegisteredPlatformDrafts(a);
    expect(aDirty).toBe(false); expect(bFlushed).toBe(false);
});

test("同ID资源元数据按工作区缓存，旧会话回执不会污染新账号", async () => {
    const gate = deferred(); const dispatched = deferred();
    let requests = 0;
    apiClient.defaults.adapter = async (config) => {
        const account = ++requests === 1 ? "A" : "B";
        if (account === "A") { dispatched.resolve(); await gate.promise; }
        return { data: { code: 0, data: { resource: { id: "same-resource", userId: account } }, msg: "" }, status: 200, statusText: "OK", headers: {}, config };
    };
    const old = getResource("same-resource").catch((error) => error);
    await dispatched.promise; activate(b); gate.resolve();
    expect(await old).toMatchObject({ name: "AbortError" });
    expect((await getResource("same-resource")).userId).toBe("B");
    expect((await getResource("same-resource")).userId).toBe("B");
    expect(requests).toBe(2);
});

test("A的Blob下载晚到不产生B可读取的Object URL", async () => {
    const gate = deferred(); const dispatched = deferred();
    apiClient.defaults.adapter = async (config) => {
        dispatched.resolve(); await gate.promise;
        return { data: new Blob(["A的图片"], { type: "image/png" }), status: 200, statusText: "OK", headers: {}, config };
    };
    const old = cacheResourceObjectUrl("resource:late-blob").catch(() => "");
    await dispatched.promise; activate(b); gate.resolve();
    expect(await old).toBe("");
    expect(peekCachedResourceObjectUrl("resource:late-blob")).toBe("");
    expect([...data.get(RESOURCE_BLOBS_STORE_NAME)!.keys()].some((key) => key.startsWith(`${b}:`))).toBe(false);
});
