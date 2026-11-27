import { http } from "./request";
import type { ModelProtocolDefinition } from "@/lib/model-protocols";
import { toProviderDefinitions, type PluginProviderCatalogItem } from "./plugin-catalog";
import type { ChannelModelCatalogItem } from "@/lib/channel-model-catalog";

export type PlatformCapability = "text" | "image" | "video" | "audio";
export type PlatformReadiness = "available" | "partially_published" | "channel_disabled" | "model_disabled" | "incomplete" | "unpublished";
export type ModelPrice = { modelId: string; variantId: string; unitCredits: number; published: boolean; version?: number };
export type PlatformVariantDraft = { id?: string; selector: Record<string, unknown>; providerModelKey: string; enabled: boolean; price?: { unitCredits: number } | null; [key: string]: unknown };
type PlatformModelFields = {
    id?: string;
    modelKey: string;
    displayName: string;
    providerModelKey: string;
    capability: PlatformCapability | "";
    protocol: string;
    enabled: boolean;
    capabilityConfig: Record<string, unknown>;
};
export type PlatformModelDraft = PlatformModelFields & { variants: PlatformVariantDraft[]; [key: string]: unknown };
export type PlatformModel = PlatformModelFields & {
    id: string;
    capability: PlatformCapability;
    variants: { id: string; selector: Record<string, unknown>; providerModelKey: string; enabled: boolean; price: ModelPrice | null; [key: string]: unknown }[];
    icon?: string;
    readiness: { state: PlatformReadiness; reason: string };
    price?: ModelPrice | null;
};
export type PlatformChannel = { id: string; name: string; baseUrl: string; enabled: boolean; hasApiKey: boolean; hasSecretKey?: boolean; hasHeaders?: boolean; totalModels: number; availableModels: number; updatedAt?: string };
export type PlatformChannelDraft = { name: string; baseUrl: string; apiKey?: string; secretKey?: string; headers?: { name: string; value: string }[]; enabled: boolean };
export type PlatformChannelPage = { channels: PlatformChannel[]; total: number; page: number; pageSize: number };
export type PlatformModelPage = { items: PlatformModel[]; total: number; page: number; pageSize: number };
export type PlatformProtocol = ModelProtocolDefinition & { capabilityConfig?: Record<string, unknown> };
export type PlatformDiscoveredModel = { id: string; displayName?: string; capability?: PlatformCapability; protocol?: string; capabilityConfig?: Record<string, unknown>; existing?: boolean };
export type PlatformProbeResult = { models: ChannelModelCatalogItem[]; existingModelIds: string[]; durationMs: number };
export type PlatformProbeInput = Partial<PlatformChannelDraft> & { channelId?: string; catalogFormat?: "openai" | "gemini" };

export const getPlatformChannels = (params: { page: number; pageSize?: number; q?: string; status?: string }, signal?: AbortSignal) => http.get<PlatformChannelPage>("/admin/channels", { params: { ...params, pageSize: params.pageSize ?? 10 }, signal });
export const savePlatformChannel = (id: string, input: Partial<PlatformChannelDraft> & { initialModels?: PlatformModelDraft[] }) => (id ? http.put<PlatformChannel>(`/admin/channels/${id}`, input) : http.post<PlatformChannel>("/admin/channels", input));
export const deletePlatformChannel = (id: string) => http.delete<{ deleted: true }>(`/admin/channels/${id}`);
export const getPlatformModels = (id: string, params: { page: number; pageSize?: number; q?: string; capability?: string; readiness?: string; status?: string }, signal?: AbortSignal) =>
    http.get<PlatformModelPage>(`/admin/channels/${id}/models`, { params: { ...params, pageSize: params.pageSize ?? 20 }, signal });
export const savePlatformModel = (channel: string, id: string, input: PlatformModelDraft) => (id ? http.put<PlatformModel>(`/admin/channels/${channel}/models/${id}`, input) : http.post<PlatformModel>(`/admin/channels/${channel}/models`, input));
export const setPlatformModelEnabled = (channel: string, id: string, enabled: boolean) => http.put<PlatformModel>(`/admin/channels/${channel}/models/${id}`, { enabled });
export const savePlatformModels = (channel: string, models: PlatformModelDraft[]) => http.post<{ items: PlatformModel[] }>(`/admin/channels/${channel}/models/batch`, { models });
export const getModelPrices = () => http.get<ModelPrice[]>("/admin/prices");
export const saveModelPrice = (price: ModelPrice) => http.put("/admin/prices", price);
export const saveModelPrices = (prices: ModelPrice[]) => http.post<{ items: ModelPrice[] }>("/admin/prices/batch", { prices });
export const probePlatformChannel = (input: PlatformProbeInput, signal?: AbortSignal) => http.post<PlatformProbeResult>("/admin/channels/probe", input, { signal, timeout: 30_000 });
export async function getPlatformProtocols(signal?: AbortSignal): Promise<PlatformProtocol[]> {
    const result = await http.get<{ providers: PluginProviderCatalogItem[] }>("/admin/protocols", { signal });
    return result.providers.flatMap((provider) => toProviderDefinitions(provider));
}
export const getPlatformModelDefaults = (input: { model: string; protocol: string; capability: PlatformCapability; capabilityConfig?: Record<string, unknown> }, signal?: AbortSignal) =>
    http.post<Record<string, unknown> | null>("/admin/model-capabilities", input, { signal });
