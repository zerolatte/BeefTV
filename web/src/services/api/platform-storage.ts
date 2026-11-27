import { http } from "./request";

export type StorageProvider = "local" | "oss";
export interface OSSDraft {
    endpoint: string;
    region: string;
    bucket: string;
    accessKeyId: string;
    accessKeySecret: string;
}
export interface PublicOSS {
    endpoint: string;
    region: string;
    bucket: string;
    hasAccessKeyId: boolean;
    hasAccessKeySecret: boolean;
}
export interface StorageSettings {
    provider: StorageProvider;
    version: number;
    oss: PublicOSS | null;
    statistics: { provider: StorageProvider; count: number; bytes: number }[];
    recentFallbacks: { resourceId: string; reason: string; createdAt: string }[];
}
export interface StorageSaveRequest {
    provider: StorageProvider;
    expectedVersion: number;
    oss?: OSSDraft;
}
export interface StorageProbeResult { elapsedMs: number; message: string }

export const getStorageSettings = (signal?: AbortSignal) => http.get<StorageSettings>("/admin/storage", { signal });
export const probeStorage = (oss?: OSSDraft, signal?: AbortSignal) => http.post<StorageProbeResult>("/admin/storage/probe", { oss }, { signal, timeout: 30000 });
export const saveStorageSettings = (draft: StorageSaveRequest, signal?: AbortSignal) => http.put<StorageSettings>("/admin/storage", draft, { signal, timeout: 35000 });
