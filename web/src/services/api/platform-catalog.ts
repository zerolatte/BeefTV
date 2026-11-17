import { http } from "@/services/api/request";
import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import type { CapabilitySpec } from "@/services/api/logical-models";

export type PlatformCapabilitySpec = CapabilitySpec;

export type PlatformCatalogModel = {
    id: string;
    code: string;
    name: string;
    icon: string;
    description: string;
    capability: string;
    sortOrder: number;
    legacyModelIds: string[];
    capabilitySpec: PlatformCapabilitySpec;
    capabilityProfiles: PlatformCapabilitySpec[];
    defaultOptions: Record<string, unknown>;
    available: boolean;
    pointPrices: Record<string, number>;
};

export type PlatformModelCatalogResponse = {
    source: "frontend";
    models: PlatformCatalogModel[];
    channels: [];
};

export type PlatformChannel = {
    id: string;
    name: string;
    publicAlias: string;
    sortOrder: number;
    concurrencyLimit: number;
    enabled: boolean;
    credentialVersionId: string;
    credentialVersion: number;
    upstreamHost: string;
    hasApiKey: boolean;
    hasSecretKey: boolean;
    headerNames: string[];
    createdAt: string;
    updatedAt: string;
};

export type PlatformChannelModel = {
    id: string;
    channelId: string;
    modelKey: string;
    providerModelKey: string;
    displayName: string;
    icon: string;
    capability: string;
    protocol: string;
    enabled: boolean;
    capabilityConfig?: ModelCapabilityConfig;
    variants?: PlatformChannelModelVariant[];
};

export type PlatformLogicalModel = {
    id: string;
    code: string;
    name: string;
    icon: string;
    description: string;
    capability: string;
    enabled: boolean;
    sortOrder: number;
    activeRevisionId: string;
    createdAt: string;
    updatedAt: string;
};

export type PlatformLogicalModelView = {
    model: PlatformLogicalModel;
    revision: { id: string; version: number; createdAt: string };
    capabilitySpec: PlatformCapabilitySpec;
    defaultOptions: Record<string, unknown>;
    routes: Array<{ id: string; channelModelId: string; enabled: boolean; priority: number; weight: number }>;
};

export type PlatformModelPrice = { id: string; logicalModelId: string; operation: string; version: number; points: number; createdAt: string };

export type PlatformChannelInput = {
    name: string;
    publicAlias: string;
    sortOrder: number;
    concurrencyLimit: number;
    enabled: boolean;
    credentials: { baseUrl: string; apiKey: string; secretKey?: string; headers?: Array<{ name: string; value: string }> };
};

export type PlatformChannelPatch = Partial<Pick<PlatformChannelInput, "name" | "publicAlias" | "sortOrder" | "concurrencyLimit" | "enabled">>;
export type PlatformChannelModelVariant = { selector?: Record<string, string>; resolution?: string; videoSeconds?: number; providerModelKey: string; enabled?: boolean };
export type PlatformChannelModelInput = { modelKey: string; providerModelKey: string; displayName: string; icon: string; capability: string; protocol: string; enabled?: boolean; capabilityConfig?: ModelCapabilityConfig; variants?: PlatformChannelModelVariant[] };
export type PlatformLogicalModelInput = { code: string; name: string; icon: string; description: string; enabled: boolean; sortOrder: number; expectedRevisionId?: string; capabilitySpec: PlatformCapabilitySpec; defaultOptions: Record<string, unknown>; routes: Array<{ channelModelId: string; enabled: boolean; priority: number; weight: number }> };

export function getPlatformModelCatalog(signal?: AbortSignal) {
    return http.get<PlatformModelCatalogResponse>("/models/catalog", { signal });
}
export function getPlatformProtocols(signal?: AbortSignal) {
    return http.get<{ protocols: Array<{ id: string; name: string; capability: string }> }>("/admin/model-protocols", { signal });
}

export function listPlatformChannels(page = 1, pageSize = 20, signal?: AbortSignal) {
    return http.get<{ channels: PlatformChannel[]; total: number; page: number; pageSize: number }>("/admin/channels", { params: { page, pageSize }, signal });
}
export function createPlatformChannel(input: PlatformChannelInput) { return http.post<{ channel: PlatformChannel }>("/admin/channels", input); }
export function updatePlatformChannel(id: string, input: PlatformChannelPatch) { return http.patch<{ channel: PlatformChannel }>(`/admin/channels/${encodeURIComponent(id)}`, input); }
export function rotatePlatformChannelCredential(id: string, input: PlatformChannelInput["credentials"]) { return http.post<{ channel: PlatformChannel }>(`/admin/channels/${encodeURIComponent(id)}/credentials`, input); }
export function listPlatformChannelModels(channelId: string, signal?: AbortSignal) { return http.get<{ models: PlatformChannelModel[] }>(`/admin/channels/${encodeURIComponent(channelId)}/models`, { signal }); }
export function createPlatformChannelModel(channelId: string, input: PlatformChannelModelInput) { return http.post<{ model: PlatformChannelModel }>(`/admin/channels/${encodeURIComponent(channelId)}/models`, input); }
export function updatePlatformChannelModel(channelId: string, modelId: string, input: PlatformChannelModelInput) { return http.put<{ model: PlatformChannelModel }>(`/admin/channels/${encodeURIComponent(channelId)}/models/${encodeURIComponent(modelId)}`, input); }

export function listPlatformLogicalModels(page = 1, pageSize = 20, signal?: AbortSignal) {
    return http.get<{ models: PlatformLogicalModel[]; total: number; page: number; pageSize: number }>("/admin/models", { params: { page, pageSize }, signal });
}
export function getPlatformLogicalModel(id: string, signal?: AbortSignal) { return http.get<PlatformLogicalModelView>(`/admin/models/${encodeURIComponent(id)}`, { signal }); }
export function publishPlatformLogicalModel(id: string | undefined, input: PlatformLogicalModelInput) { return id ? http.put<PlatformLogicalModelView>(`/admin/models/${encodeURIComponent(id)}`, input) : http.post<PlatformLogicalModelView>("/admin/models", input); }
export function listPlatformModelPrices(id: string, signal?: AbortSignal) { return http.get<{ prices: PlatformModelPrice[] }>(`/admin/models/${encodeURIComponent(id)}/prices`, { signal }); }
export function publishPlatformModelPrice(id: string, operation: string, points: number) { return http.post<{ price: PlatformModelPrice }>(`/admin/models/${encodeURIComponent(id)}/prices`, { operation, points }); }
export function getPlatformModelGrants(accountId: string, signal?: AbortSignal) { return http.get<{ logicalModelIds: string[] }>(`/admin/users/${encodeURIComponent(accountId)}/model-grants`, { signal }); }
export function replacePlatformModelGrants(accountId: string, logicalModelIds: string[]) { return http.put<{ logicalModelIds: string[] }>(`/admin/users/${encodeURIComponent(accountId)}/model-grants`, { logicalModelIds }); }
