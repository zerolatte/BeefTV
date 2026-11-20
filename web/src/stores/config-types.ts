import type { ModelProtocol } from "@/lib/model-protocols";
import type { ModelCapabilityConfig } from "@/lib/model-capabilities";
import type { MediaCapabilities } from "@/lib/media-capabilities";
import type { VideoPriceQuote } from "@/lib/seedance-portrait";
import type { CapabilitySpec } from "@/services/api/logical-models";

export type ApiCallFormat = "openai" | "gemini" | "claude";
export type ChannelInterfaceType = ModelProtocol;
export type ChannelHeader = { name: string; value: string };
export type ReferenceMediaTransport = Partial<Record<"image" | "video" | "audio", "auto" | "inline" | "url" | "link">>;


export type ModelChannel = {
    id: string;
    name: string;
    publicAlias?: string;
    sortOrder?: number;
    baseUrl: string;
    referenceAssetOrigin?: string;
    apiKey: string;
    secretKey?: string;
    headers?: ChannelHeader[];
    apiFormat: ApiCallFormat;
    interfaceType?: ChannelInterfaceType;
    models: string[];
    // 仅平台目录使用：将已保存的旧 SKU 选择重定向到当前模型家族。
    modelAliases?: Record<string, string>;
    scope?: "system" | "user";
    enabled?: boolean;
    pinned?: boolean;
    // 这个服务以前用过的 ID，由后端给出。老画布和老设置里存的「旧ID::模型」按这个服务读。
    legacyIds?: string[];
    presetVersion?: number;
    credentialRef?: string;
    ownerPluginId?: string;
    hasApiKey?: boolean;
    hasSecretKey?: boolean;
    concurrencyLimit?: number;
    modelProfiles?: Array<{
        model: string;
        displayName?: string;
        description?: string;
        icon?: string;
        capability: ModelCapability;
        protocol?: ModelProtocol;
        referenceMediaTransport?: ReferenceMediaTransport;
        authType?: string;
        capabilityConfig?: ModelCapabilityConfig;
        /** "user" 表示能力参数由用户在「能力与参数」里亲手改过；未标记的配置只是界面默认值，不能拿来拦截生成。 */
        capabilitySource?: "user";
        imageCapabilitiesVersion?: string;
        videoCapabilitiesVersion?: string;
        mediaCapabilities?: MediaCapabilities;
        mediaCapabilitiesVersion?: string;
        videoPricing?: VideoPriceQuote | null;
        logicalModelId?: string;
        logicalCapabilitySpec?: CapabilitySpec;
        logicalCapabilityProfiles?: CapabilitySpec[];
        defaultOptions?: Record<string, unknown>;
    }>;
};

export type AiConfig = {
    channelMode: "remote";
    baseUrl: string;
    apiKey: string;
    apiFormat: ApiCallFormat;
    channels: ModelChannel[];
    model: string;
    imageModel: string;
    videoModel: string;
    textModel: string;
    audioModel: string;
    /** 画布助手模型；为空表示跟随 `textModel`。取值与其他默认模型一致，形如 `channelId::modelId`。 */
    assistantModel: string;
    /** 默认影音理解模型；为空表示不使用独立多模态模型。 */
    mediaReviewModel: string;
    audioVoice: string;
    audioFormat: string;
    audioSpeed: string;
    audioPitch: string;
    audioVolume: string;
    audioInstructions: string;
    videoSeconds: string;
    vquality: string;
    videoGenerateAudio: string;
    videoWatermark: string;
    videoArkPrivateAssetUpload: string; videoDraft: string; videoReturnLastFrame?: string;
    systemPrompt: string;
    models: string[];
    imageModels: string[];
    videoModels: string[];
    textModels: string[];
    audioModels: string[];
    quality: string;
    size: string;
    transparentBackground: string;
    seedreamMode?: string;
    seedreamMaxImages?: string;
    seedreamOutputFormat?: string;
    seedreamOptimize?: string;
    seedreamWatermark?: string;
    count: string;
    canvasImageCount: string;
};

export type ModelCapability = "image" | "video" | "text" | "audio";
