import { expect, test } from "bun:test";
import {
    appendPlatformModelDrafts,
    createPlatformModelDraft,
    createPlatformVariant,
    mergeModelConfig,
    modelConfigVisualError,
    parsePlatformModelJSON,
    PlatformDraftError,
    parseManualModelKeys,
    platformModelSavePayload,
    platformModelToDraft,
    resolvePlatformDraftDefaults,
    validatePlatformModelDrafts,
} from "../src/pages/platform/model-admin-drafts";
import type { PlatformModel, PlatformModelDraft } from "../src/services/api/platform-models";

function configuredDraft(key = "example-model"): PlatformModelDraft {
    return { ...createPlatformModelDraft(key), capability: "image", protocol: "openai-image", variants: [createPlatformVariant()] };
}

function publishedModel(): PlatformModel {
    return {
        ...configuredDraft(),
        id: "model-existing",
        capability: "image",
        readiness: { state: "available", reason: "可用" },
        variants: [{ id: "variant-existing", selector: { quality: "high" }, providerModelKey: "upstream-existing", enabled: true, price: { modelId: "model-existing", variantId: "variant-existing", unitCredits: 8, published: true, version: 4 } }],
    };
}

test("新增模型不从名称猜能力、协议或积分价格", () => {
    const draft = createPlatformModelDraft("video-image-text-v2");
    expect(draft.displayName).toBe(draft.modelKey);
    expect(draft.capability).toBe("");
    expect(draft.protocol).toBe("");
    expect(draft.variants.every((variant) => variant.price == null)).toBe(true);
    expect(validatePlatformModelDrafts([draft])?.field).toBe("capability");
});

test("批量录入按行与逗号分隔、去空白去重，追加不覆盖已编辑草稿", () => {
    expect(parseManualModelKeys(" model-a\nmodel-b，model-a, \n model-c ")).toEqual(["model-a", "model-b", "model-c"]);
    const existing = { ...configuredDraft("model-a"), displayName: "管理员已编辑名称" };
    const result = appendPlatformModelDrafts([existing], [createPlatformModelDraft("model-a"), createPlatformModelDraft("model-b"), createPlatformModelDraft("model-b")]);
    expect(result.map((model) => model.modelKey)).toEqual(["model-a", "model-b"]);
    expect(result[0]).toBe(existing);
    expect(result[0].displayName).toBe("管理员已编辑名称");
});

test("能力可视化更新保留未知扩展字段并保持输入不可变", () => {
    const original = { version: 1, image: { maxOutputs: 1, references: { maxImages: 4, extension: "keep" }, future: { enabled: true } }, extension: ["a"] };
    const snapshot = structuredClone(original);
    const result = mergeModelConfig(original, { image: { maxOutputs: 2, references: { maxImages: 6 } } });
    expect(result).toEqual({ version: 1, image: { maxOutputs: 2, references: { maxImages: 6, extension: "keep" }, future: { enabled: true } }, extension: ["a"] });
    expect(original).toEqual(snapshot);
});

test("空价格不等同零积分，零积分必须显式输入且小数无效", () => {
    const draft = configuredDraft();
    expect(validatePlatformModelDrafts([draft])).toBeNull();
    expect(platformModelSavePayload(draft).variants[0].price).toBeUndefined();
    draft.variants[0].price = { unitCredits: 0 };
    expect(validatePlatformModelDrafts([draft])).toBeNull();
    expect(platformModelSavePayload(draft).variants[0].price).toEqual({ unitCredits: 0 });
    for (const invalid of [-1, 0.5, 1_000_000_001, Number.NaN]) {
        draft.variants[0].price = { unitCredits: invalid };
        expect(validatePlatformModelDrafts([draft])?.field).toBe("variants");
    }
});

test("重复模型与顺序不同但条件相同的重复规格定位到真实错误项", () => {
    expect(validatePlatformModelDrafts([configuredDraft("same"), configuredDraft("same")])).toMatchObject({ modelIndex: 1, field: "modelKey" });
    const draft = configuredDraft();
    draft.variants = [
        { ...createPlatformVariant(), selector: { quality: "high", size: "1024x1024" } },
        { ...createPlatformVariant(), selector: { size: "1024x1024", quality: "high" } },
    ];
    expect(validatePlatformModelDrafts([draft])).toMatchObject({ modelIndex: 0, field: "variants" });
});

test("普通模型编辑不重复写入未修改的价格或发布状态", () => {
    const original = publishedModel();
    const draft = platformModelToDraft(original);
    draft.displayName = "新的显示名称";
    draft.variants[0].providerModelKey = "upstream-renamed";
    const payload = platformModelSavePayload(draft, original);
    expect(payload.variants[0].price).toBeUndefined();
    expect(payload.variants[0].providerModelKey).toBe("upstream-renamed");
    expect(original.variants[0].providerModelKey).toBe("upstream-existing");
    draft.variants[0].price = { unitCredits: 10 };
    expect(platformModelSavePayload(draft, original).variants[0].price).toEqual({ unitCredits: 10 });
});

test("修改规格条件后同价也要显式提交，为新规格保存未发布价格", () => {
    const original = publishedModel();
    const draft = platformModelToDraft(original);
    draft.variants[0].selector = { quality: "medium" };
    expect(platformModelSavePayload(draft, original).variants[0].price).toEqual({ unitCredits: 8 });
    expect(platformModelSavePayload(draft, original).variants[0].price).not.toHaveProperty("published");
});

test("从模型读取编辑草稿时克隆能力与规格，不直接改变查询缓存", () => {
    const original = publishedModel();
    original.capabilityConfig = { version: 1, image: { maxOutputs: 1 } };
    const snapshot = structuredClone(original);
    const draft = platformModelToDraft(original);
    (draft.capabilityConfig.image as { maxOutputs: number }).maxOutputs = 2;
    draft.variants[0].selector.quality = "medium";
    expect(original).toEqual(snapshot);
});

test("接口返回的通配分辨率不会给文本、图片和音频模型添加视频条件", () => {
    for (const capability of ["text", "image", "audio"] as const) {
        const original = publishedModel();
        original.capability = capability;
        original.variants[0].resolution = "*";
        original.variants[0].videoSeconds = "*";
        const draft = platformModelToDraft(original);
        expect(draft.variants[0].selector).toEqual({ quality: "high" });
        expect(platformModelSavePayload(draft, original).variants[0].price).toBeUndefined();
        expect(platformModelSavePayload(draft, original).variants[0]).not.toHaveProperty("resolution");
    }
});

test("切换能力后为缺失的目标分支补服务端默认值，保留已经填写的其他分支", async () => {
    const draft = configuredDraft();
    draft.capabilityConfig = { version: 1, text: { references: { promptMaxChars: 1200 } } };
    const calls: unknown[] = [];
    const result = await resolvePlatformDraftDefaults([draft], async (input) => {
        calls.push(input);
        return { version: 1, image: { maxOutputs: 4, references: { maxImages: 3 } } };
    });
    expect(calls).toHaveLength(1);
    expect(result[0].capabilityConfig).toEqual({ version: 1, text: { references: { promptMaxChars: 1200 } }, image: { maxOutputs: 4, references: { maxImages: 3 } } });
    expect(draft.capabilityConfig).not.toHaveProperty("image");
});

test("高级配置中的可选规格和数组类型错误返回可读错误，不进入可视化组件", () => {
    const defaults = { version: 1, image: { size: { values: ["1024x1024"] } } };
    expect(modelConfigVisualError({ image: { size: { presets: {} } } }, defaults)).toContain("presets");
    expect(modelConfigVisualError({ image: { size: { presets: [null] } } }, defaults)).toContain("presets");
    expect(modelConfigVisualError({ image: { size: { values: 1 } } }, defaults)).toContain("values");
    expect(modelConfigVisualError({ video: { resolutions: [null] } }, defaults)).toContain("resolutions");
    const extended = { ...defaults, future: { values: [null] } };
    expect(modelConfigVisualError(extended, defaults)).toBe("");
    expect(extended.future.values).toEqual([null]);
});

test("高级模型JSON明确拒绝只读字段和发布字段，避免保存时静默忽略", () => {
    const draft = configuredDraft();
    expect(() => parsePlatformModelJSON(JSON.stringify({ ...draft, unknownField: true }), draft)).toThrow("不支持的模型字段");
    expect(() => parsePlatformModelJSON(JSON.stringify({ ...draft, variants: [{ ...draft.variants[0], resolution: "720p" }] }), draft)).toThrow("不支持的规格字段");
    expect(() => parsePlatformModelJSON(JSON.stringify({ ...draft, variants: [{ ...draft.variants[0], price: { unitCredits: 2, published: true } }] }), draft)).toThrow("发布状态");
});

test("服务端能力校验失败定位到对应草稿，取消请求保持取消状态", async () => {
    const drafts = [configuredDraft("first"), configuredDraft("second")];
    try {
        await resolvePlatformDraftDefaults(drafts, async ({ model }) => {
            if (model === "second") throw new Error("尺寸配置无效");
            return {};
        });
        throw new Error("应该拒绝无效能力配置");
    } catch (error) {
        expect(error).toBeInstanceOf(PlatformDraftError);
        expect((error as PlatformDraftError).modelIndex).toBe(1);
        expect((error as Error).message).toContain("第 2 个模型：尺寸配置无效");
    }
    const cancelled = new DOMException("请求已取消", "AbortError");
    await expect(
        resolvePlatformDraftDefaults(drafts, async () => {
            throw cancelled;
        }),
    ).rejects.toBe(cancelled);
});
