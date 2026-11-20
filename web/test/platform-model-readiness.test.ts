import { afterEach, expect, test } from "bun:test";
import { setWorkspaceCapabilitySnapshot, resetWorkspaceMode } from "../src/services/workspace-mode";
import { defaultConfig, createModelChannel, encodeChannelModel, useConfigStore } from "../src/stores/use-config-store";
import { defaultModelCapabilityConfig } from "../src/lib/model-capabilities";
import { listVideoReferenceModels } from "../src/lib/canvas/canvas-video-regeneration";

afterEach(resetWorkspaceMode);

test("平台目录无需浏览器密钥和上游地址即可选择模型", () => {
    setWorkspaceCapabilitySnapshot({ contractVersion: 1, profile: "platform", capabilities: { localAssets: true, providerCalls: true } });
    const capability = defaultModelCapabilityConfig("newapi-channel-2", "test-video");
    if (capability.video) {
        capability.video.references.maxVideos = 1;
        capability.video.operations = ["reference_to_video"];
    }
    const channel = createModelChannel({ id: "platform-channel", scope: "system", models: ["test-video"], modelProfiles: [{ model: "test-video", capability: "video", protocol: "newapi-channel-2", capabilityConfig: capability }] });
    channel.baseUrl = "";
    channel.apiKey = "";
    const model = encodeChannelModel(channel.id, "test-video");
    const config = { ...defaultConfig, channels: [channel], model, videoModel: model };
    expect(useConfigStore.getState().isAiConfigReady(config, model)).toBe(true);
    expect(listVideoReferenceModels(config)).toEqual([model]);
    resetWorkspaceMode();
    expect(useConfigStore.getState().isAiConfigReady(config, model)).toBe(false);
});
