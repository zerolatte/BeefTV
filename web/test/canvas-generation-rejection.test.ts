import { expect, test } from "bun:test";
import { restoreRejectedCanvasGenerationNode } from "../src/lib/canvas/canvas-generation-rejection";
import { clearInPlaceMediaOutputMetadata } from "../src/lib/canvas/canvas-project-generation-metadata";
import { CanvasNodeType, type CanvasNodeData } from "../src/types/canvas";

const original: CanvasNodeData = { id: "image", type: CanvasNodeType.Image, title: "旧作品", position: { x: 0, y: 0 }, width: 320, height: 200, metadata: { content: "/old.png", storageKey: "old-resource", status: "success", taskId: "old-task", taskStatus: "succeeded", taskClientOperationId: "old-operation", composerContent: "原提示词" } };

test("准入拒绝恢复旧作品和任务绑定，保留用户当前编辑与位置", () => {
    const current = { ...original, position: { x: 50, y: 40 }, metadata: { ...clearInPlaceMediaOutputMetadata(original.metadata), composerContent: "新提示词", taskClientOperationId: "new-operation", generationSubmissionState: "rejected" as const } };
    const restored = restoreRejectedCanvasGenerationNode(current, original);
    expect(restored.metadata).toMatchObject({ content: "/old.png", storageKey: "old-resource", taskId: "old-task", taskStatus: "succeeded", status: "success", composerContent: "新提示词" });
    expect(restored.position).toEqual({ x: 50, y: 40 });
});

test("同批已受理或成功的其他任务不回退", () => {
    const accepted = { ...original, metadata: { status: "loading" as const, taskId: "new-task", generationSubmissionState: "accepted" as const } };
    expect(restoreRejectedCanvasGenerationNode(accepted, original)).toBe(accepted);
});

test("未产生任务的新节点回到可编辑状态并清理拒绝身份", () => {
    const restored = restoreRejectedCanvasGenerationNode({ ...original, metadata: { status: "loading", taskClientOperationId: "new-operation", generationSubmissionState: "rejected", taskStage: "正在提交", composerContent: "保留输入" } });
    expect(restored.metadata).toMatchObject({ status: "idle", composerContent: "保留输入" });
    expect(restored.metadata?.taskClientOperationId).toBeUndefined();
    expect(restored.metadata?.taskStage).toBeUndefined();
});
