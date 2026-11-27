import type { PlatformModel, PlatformModelDraft, PlatformVariantDraft, PlatformCapability } from "@/services/api/platform-models";

export const MODEL_CAPABILITIES = [
    { value: "text", label: "文本" },
    { value: "image", label: "图片" },
    { value: "video", label: "视频" },
    { value: "audio", label: "音频" },
] as const;

export type ModelDraftIssue = { modelIndex: number; field: string; message: string };

export class PlatformDraftError extends Error {
    constructor(
        readonly modelIndex: number,
        message: string,
        cause: unknown,
    ) {
        super(`第 ${modelIndex + 1} 个模型：${message}`, { cause });
        this.name = "PlatformDraftError";
    }
}

/** 草稿保留服务端扩展字段，新增模型和规格从未发布状态开始。 */
export function createPlatformModelDraft(modelKey = ""): PlatformModelDraft {
    return { modelKey, displayName: modelKey, providerModelKey: "", capability: "", protocol: "", enabled: true, capabilityConfig: {}, variants: [] };
}

export function platformModelToDraft(model: PlatformModel): PlatformModelDraft {
    return structuredClone({
        id: model.id,
        modelKey: model.modelKey,
        providerModelKey: model.providerModelKey || "",
        displayName: model.displayName,
        ...(model.icon !== undefined ? { icon: model.icon } : {}),
        capability: model.capability,
        protocol: model.protocol,
        enabled: model.enabled,
        capabilityConfig: model.capabilityConfig || {},
        variants: model.variants.length
            ? model.variants.map((variant) => ({
                  id: variant.id,
                  providerModelKey: variant.providerModelKey || "",
                  enabled: variant.enabled,
                  selector: {
                      ...(model.capability === "video" && variant.resolution && variant.resolution !== "*" && !variant.selector.vquality ? { vquality: String(variant.resolution) } : {}),
                      ...(model.capability === "video" && variant.videoSeconds && variant.videoSeconds !== "*" && !variant.selector.videoSeconds ? { videoSeconds: String(variant.videoSeconds) } : {}),
                      ...variant.selector,
                  },
                  price: variant.price ? { unitCredits: variant.price.unitCredits } : null,
              }))
            : [{ ...createPlatformVariant(), price: model.price ? { unitCredits: model.price.unitCredits } : null }],
    });
}

export function parseManualModelKeys(value: string): string[] {
    return [
        ...new Set(
            value
                .split(/[\n,，]/)
                .map((item) => item.trim())
                .filter(Boolean),
        ),
    ];
}

export function appendPlatformModelDrafts(current: PlatformModelDraft[], incoming: PlatformModelDraft[]): PlatformModelDraft[] {
    const seen = new Set(current.map((model) => model.modelKey.trim()));
    return [
        ...current,
        ...incoming.filter((model) => {
            const key = model.modelKey.trim();
            if (!key || seen.has(key)) return false;
            seen.add(key);
            return true;
        }),
    ];
}

/** 可视化表单只更新自己的字段，未知字段留给服务端和高级编辑。 */
export function mergeModelConfig(current: Record<string, unknown>, next: Record<string, unknown>): Record<string, unknown> {
    const result = { ...current };
    for (const [key, value] of Object.entries(next)) {
        const previous = result[key];
        result[key] = isRecord(previous) && isRecord(value) ? mergeModelConfig(previous, value) : value;
    }
    return result;
}

export function isRecord(value: unknown): value is Record<string, unknown> {
    return Boolean(value && typeof value === "object" && !Array.isArray(value));
}

export function validatePlatformModelDrafts(models: PlatformModelDraft[]): ModelDraftIssue | null {
    const seen = new Set<string>();
    for (let modelIndex = 0; modelIndex < models.length; modelIndex++) {
        const model = models[modelIndex];
        const fail = (field: string, message: string): ModelDraftIssue => ({ modelIndex, field, message: `第 ${modelIndex + 1} 个模型：${message}` });
        if (typeof model.modelKey !== "string" || !model.modelKey.trim()) return fail("modelKey", "请填写模型标识");
        if (seen.has(model.modelKey.trim())) return fail("modelKey", "模型标识重复");
        seen.add(model.modelKey.trim());
        if (typeof model.displayName !== "string" || !model.displayName.trim()) return fail("displayName", "请填写显示名称");
        if (!MODEL_CAPABILITIES.some((item) => item.value === model.capability)) return fail("capability", "请选择能力");
        if (typeof model.protocol !== "string" || !model.protocol.trim()) return fail("protocol", "请选择请求协议");
        if (!isRecord(model.capabilityConfig)) return fail("capabilityConfig", "能力配置必须是 JSON 对象");
        if (!Array.isArray(model.variants)) return fail("variants", "规格必须是数组");
        const selectors = new Set<string>();
        for (let index = 0; index < model.variants.length; index++) {
            const variant = model.variants[index];
            if (!isRecord(variant) || !isRecord(variant.selector)) return fail("variants", `第 ${index + 1} 个规格的条件必须是对象`);
            if (Object.entries(variant.selector).some(([key, value]) => !key.trim() || typeof value !== "string")) return fail("variants", `第 ${index + 1} 个规格的条件名称和值必须是文本`);
            const selector = JSON.stringify(Object.entries(variant.selector).sort(([a], [b]) => a.localeCompare(b)));
            if (selectors.has(selector)) return fail("variants", `第 ${index + 1} 个规格的条件重复`);
            selectors.add(selector);
            if (variant.price && (!Number.isSafeInteger(variant.price.unitCredits) || variant.price.unitCredits < 0 || variant.price.unitCredits > 1_000_000_000)) return fail("variants", `第 ${index + 1} 个规格的积分必须为 0 至 1000000000 的整数`);
        }
    }
    return null;
}

export function parsePlatformModelJSON(text: string, current: PlatformModelDraft): PlatformModelDraft {
    const parsed: unknown = JSON.parse(text);
    if (
        !isRecord(parsed) ||
        typeof parsed.modelKey !== "string" ||
        typeof parsed.displayName !== "string" ||
        typeof parsed.providerModelKey !== "string" ||
        typeof parsed.protocol !== "string" ||
        typeof parsed.enabled !== "boolean" ||
        !isRecord(parsed.capabilityConfig) ||
        !Array.isArray(parsed.variants)
    )
        throw new Error("模型标识、名称、协议须为文本，能力配置须为对象，规格须为数组");
    if (!["", "text", "image", "video", "audio"].includes(String(parsed.capability))) throw new Error("模型能力无效");
    const supportedKeys = new Set(["id", "modelKey", "providerModelKey", "displayName", "icon", "capability", "protocol", "enabled", "capabilityConfig", "variants"]);
    const unknownKey = Object.keys(parsed).find((key) => !supportedKeys.has(key));
    if (unknownKey) throw new Error(`不支持的模型字段：${unknownKey}，请检查字段名称`);
    if (
        parsed.variants.some(
            (item) =>
                !isRecord(item) ||
                !isRecord(item.selector) ||
                typeof item.enabled !== "boolean" ||
                (item.providerModelKey !== undefined && typeof item.providerModelKey !== "string") ||
                (item.price !== null && item.price !== undefined && (!isRecord(item.price) || typeof item.price.unitCredits !== "number")),
        )
    )
        throw new Error("规格须包含条件对象、启用状态和有效价格");
    const supportedVariantKeys = new Set(["id", "selector", "providerModelKey", "enabled", "price"]);
    for (const item of parsed.variants as Record<string, unknown>[]) {
        const unknownVariant = Object.keys(item).find((key) => !supportedVariantKeys.has(key));
        if (unknownVariant) throw new Error(`不支持的规格字段：${unknownVariant}`);
        if (isRecord(item.price) && Object.keys(item.price).some((key) => key !== "unitCredits")) throw new Error("规格价格只支持 unitCredits，发布状态请在价格与发布中调整");
    }
    const clearedPrice = parsed.variants.some((item) => isRecord(item) && !item.price && current.variants.some((previous) => previous.id && previous.id === item.id && previous.price));
    if (clearedPrice) throw new Error("已有价格不能清空，可在价格与发布中关闭发布状态");
    return { ...current, ...parsed } as PlatformModelDraft;
}

/** 已加载完整默认值后检查可视化编辑依赖的形状；非法 JSON 留在高级区修正。 */
export function modelConfigVisualError(config: Record<string, unknown>, defaults: Record<string, unknown>): string {
    const arrays = [
        ["image.size.values", "string"],
        ["image.quality.values", "string"],
        ["image.generationOutputs", "number"],
        ["video.operations", "string"],
        ["video.ratios", "string"],
        ["video.resolutions", "string"],
        ["video.duration.values", "number"],
    ] as const;
    const read = (path: string): unknown => path.split(".").reduce<unknown>((value, key) => (isRecord(value) ? value[key] : undefined), config);
    for (const [path, type] of arrays) {
        const value = read(path);
        if (value === undefined) continue;
        if (!Array.isArray(value)) return `能力配置.${path} 必须为数组`;
        if (value.some((item) => typeof item !== type)) return `能力配置.${path} 必须为${type === "string" ? "文本" : "数值"}数组`;
    }
    const presets = read("image.size.presets");
    if (presets !== undefined) {
        if (!Array.isArray(presets)) return "能力配置.image.size.presets 必须为数组";
        if (presets.some((item) => !isRecord(item) || ["tier", "ratio", "size"].some((key) => typeof item[key] !== "string") || ["width", "height"].some((key) => typeof item[key] !== "number")))
            return "能力配置.image.size.presets 每项须包含文本档位、比例、尺寸及数值宽高";
    }
    return modelConfigShapeError(config, defaults, "能力配置");
}

function modelConfigShapeError(config: Record<string, unknown>, defaults: Record<string, unknown>, path: string): string {
    for (const [key, expected] of Object.entries(defaults)) {
        const actual = config[key];
        if (actual === undefined) continue;
        if (Array.isArray(expected)) {
            if (!Array.isArray(actual)) return `${path}.${key} 必须为数组`;
        } else if (isRecord(expected)) {
            if (!isRecord(actual)) return `${path}.${key} 必须为对象`;
            const nested = modelConfigShapeError(actual, expected, `${path}.${key}`);
            if (nested) return nested;
        } else if (expected !== null && typeof actual !== typeof expected) return `${path}.${key} 的值类型无效`;
    }
    return "";
}

/** 保存前补齐每个草稿的服务端能力，不依赖用户是否打开过它的编辑区域。 */
export async function resolvePlatformDraftDefaults(
    models: PlatformModelDraft[],
    fetchDefaults: (input: { model: string; capability: PlatformCapability; protocol: string; capabilityConfig?: Record<string, unknown> }, signal?: AbortSignal) => Promise<Record<string, unknown> | null>,
    signal?: AbortSignal,
): Promise<PlatformModelDraft[]> {
    const resolved: PlatformModelDraft[] = [];
    for (const [index, model] of models.entries()) {
        if (!model.capability || !model.protocol || model.capability === "audio") {
            resolved.push(model);
            continue;
        }
        try {
            const config = await fetchDefaults({ model: model.modelKey, capability: model.capability, protocol: model.protocol, capabilityConfig: model.capabilityConfig }, signal);
            resolved.push({ ...model, capabilityConfig: mergeModelConfig(config || {}, model.capabilityConfig) });
        } catch (error) {
            if (signal?.aborted || (error instanceof DOMException && error.name === "AbortError")) throw error;
            throw new PlatformDraftError(index, error instanceof Error ? error.message : "无法读取能力配置", error);
        }
    }
    return resolved;
}

export function createPlatformVariant(): PlatformVariantDraft {
    return { selector: {}, providerModelKey: "", enabled: true, price: null };
}

/** 空价格表示尚未设置；已有价格未修改时省略，避免刷新价格版本。 */
export function platformModelSavePayload(draft: PlatformModelDraft, original?: PlatformModel): PlatformModelDraft {
    const variants = draft.variants.map((variant) => {
        const saved = original?.variants.find((item) => item.id === variant.id);
        const { price } = variant;
        const rest = { selector: variant.selector, providerModelKey: variant.providerModelKey, enabled: variant.enabled };
        const sameSelector = saved && selectorIdentity(saved.selector) === selectorIdentity(variant.selector);
        return price && (!saved?.price || !sameSelector || saved.price.unitCredits !== price.unitCredits) ? { ...rest, price } : rest;
    });
    return {
        modelKey: draft.modelKey,
        providerModelKey: draft.providerModelKey,
        displayName: draft.displayName,
        ...(draft.icon !== undefined ? { icon: draft.icon } : {}),
        capability: draft.capability,
        protocol: draft.protocol,
        enabled: draft.enabled,
        capabilityConfig: draft.capabilityConfig,
        variants,
    };
}

function selectorIdentity(selector: Record<string, unknown>) {
    return JSON.stringify(Object.entries(selector).sort(([a], [b]) => a.localeCompare(b)));
}

export function selectorLabel(selector: Record<string, unknown>): string {
    return (
        Object.entries(selector)
            .map(([key, value]) => `${key}: ${String(value)}`)
            .join(" · ") || "默认规格"
    );
}
