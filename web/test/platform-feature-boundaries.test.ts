import { afterEach, beforeEach, describe, expect, test } from "bun:test";
import { isWorkspaceFeatureEnabled, type WorkspaceFeature } from "@/lib/workspace-features";
import { getRuntimeMode, setRuntimeMode } from "@/services/workspace-mode";
import { useUserStore, defaultFeatureAvailability } from "@/stores/use-user-store";
import { apiClient } from "@/services/api/request";
import { assertGenerationAvailable } from "@/services/generation-availability";
import { appendTaskTextDelta, completeTextReplayTask, createGenerationTask, retryGenerationTask } from "@/services/api/task-center";
import { createDepthCaptureTask } from "@/services/api/depth-capture";
import { createUpscaleTask } from "@/services/api/media-upscale";
import { createTimelineTranscriptionTask } from "@/services/api/timeline-tasks";
import { retryWorkflowRun, startWorkflowRun } from "@/services/api/canvas-workflows";
import { extractResourceVideoFrames, updateCanvasDocumentLimitMB } from "@/services/api/resources";
import { prepareBackendToolGenerationTask, runBackendGenerationTaskBatch, submitBackendGenerationTask } from "@/services/api/generation-task";
import { requestImageQuestion } from "@/services/api/image";
import type { AiConfig } from "@/stores/use-config-store";

const originalMode = getRuntimeMode();
const originalFeatures = useUserStore.getState().features;
const originalAdapter = apiClient.defaults.adapter;
let requests = 0;

beforeEach(() => {
    requests = 0;
    setRuntimeMode("platform");
    useUserStore.getState().setFeatures({ ...defaultFeatureAvailability, generationEnabled: false });
    apiClient.defaults.adapter = async () => { requests += 1; throw new Error("平台门禁不应发出网络请求"); };
});

afterEach(() => {
    apiClient.defaults.adapter = originalAdapter;
    setRuntimeMode(originalMode);
    useUserStore.getState().setFeatures(originalFeatures);
});

describe("平台前端能力边界", () => {
    test("本地专用能力即使误报开启也不显示平台入口", () => {
        const closed: WorkspaceFeature[] = ["assistantEnabled", "proposalEnabled", "pluginsEnabled", "skillsEnabled", "eagleEnabled", "localAgentEnabled", "creationEnabled", "pluginCenterEnabled", "customChannelsEnabled"];
        for (const feature of closed) {
            expect(isWorkspaceFeatureEnabled(feature, defaultFeatureAvailability, false)).toBe(false);
            expect(isWorkspaceFeatureEnabled(feature, defaultFeatureAvailability, true)).toBe(true);
        }
        expect(isWorkspaceFeatureEnabled("shortDramaEnabled", defaultFeatureAvailability, false)).toBe(true);
        expect(isWorkspaceFeatureEnabled("generationEnabled", { ...defaultFeatureAvailability, generationEnabled: undefined }, false)).toBe(false);
        expect(isWorkspaceFeatureEnabled("generationEnabled", defaultFeatureAvailability, false)).toBe(true);
    });

    test("主任务、重试、专项和工作流在请求前拒绝生成", async () => {
        const calls = [
            () => createGenerationTask({ prompt: "测试" }),
            () => retryGenerationTask("task-a"),
            () => createDepthCaptureTask({ resourceId: "resource-a" }),
            () => createUpscaleTask({ resourceId: "resource-a", channelId: "channel-a", mode: "precision", model: "model-a", scale: 2, clientOperationId: "operation-a", projectId: "canvas-a", nodeId: "node-a" }),
            () => createTimelineTranscriptionTask({ resourceId: "resource-a" }),
            () => startWorkflowRun("workflow-a", { clientOperationId: "operation-a", inputs: {} }),
            () => retryWorkflowRun("run-a", { clientOperationId: "retry-a" }),
        ];
        for (const call of calls) await expect(Promise.resolve().then(call)).rejects.toMatchObject({ status: 403, reason: "generation_disabled", retryable: false });
        expect(requests).toBe(0);
    });

    test("批量与参考准备在读取素材或模型配置前被阻止", async () => {
        const config = {} as AiConfig;
        await expect(submitBackendGenerationTask({ mode: "image", prompt: "测试", config })).rejects.toMatchObject({ reason: "generation_disabled" });
        await expect(runBackendGenerationTaskBatch({ mode: "image", prompt: "测试", config, count: 3 })).rejects.toMatchObject({ reason: "generation_disabled" });
        expect(() => prepareBackendToolGenerationTask({ prompt: "测试", config, messages: [], tools: [], toolChoice: "auto" })).toThrow("平台生成尚未开放");
        expect(requests).toBe(0);
    });

    test("即使生成开放，客户端仍不能执行文本或宣布文本任务完成", async () => {
        useUserStore.getState().setFeatures(defaultFeatureAvailability);
        expect(assertGenerationAvailable).not.toThrow();
        expect(() => appendTaskTextDelta("task-a", "内容")).toThrow("平台文本任务由服务端执行");
        expect(() => completeTextReplayTask("task-a", "内容")).toThrow("平台文本任务由服务端执行");
        await expect(requestImageQuestion({} as AiConfig, [], () => undefined)).rejects.toMatchObject({ reason: "client_text_execution_disabled" });
        expect(requests).toBe(0);
    });

    test("尚未开放的资源派生和全局额度修改不能降级为成功", async () => {
        const denied = { status: 403, permanent: true };
        await expect(extractResourceVideoFrames("resource-a", { preset: "first" })).rejects.toMatchObject(denied);
        await expect(updateCanvasDocumentLimitMB(100)).rejects.toMatchObject(denied);
        expect(requests).toBe(0);
    });

    test("本地模式不受平台生成验收开关影响", () => {
        setRuntimeMode("local");
        expect(assertGenerationAvailable).not.toThrow();
    });
});
