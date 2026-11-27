import type { CanvasNodeData, CanvasNodeMetadata } from "@/types/canvas";
import { clearInPlaceMediaOutputMetadata, resetGenerationTaskMetadata } from "./canvas-project-generation-metadata";

/** 准入拒绝没有新任务；只恢复本次覆盖的结果与任务状态，保留用户当前编辑。 */
export function restoreRejectedCanvasGenerationNode(node: CanvasNodeData, original?: CanvasNodeData): CanvasNodeData {
    if (node.metadata?.taskId && node.metadata.taskId !== original?.metadata?.taskId && node.metadata.generationSubmissionState !== "rejected") return node;
    const before = original?.metadata || {};
    const metadata = resetGenerationTaskMetadata(node.metadata, before.status || "idle");
    const clearedBefore = clearInPlaceMediaOutputMetadata(before);
    for (const key of Object.keys(before) as Array<keyof CanvasNodeMetadata>) {
        if (!Object.hasOwn(clearedBefore, key)) Object.assign(metadata, { [key]: before[key] });
    }
    for (const key of ["status", "errorDetails", "generationErrorCode", "generationErrorSummary", "generationProviderCode", "generationPreflightError", "failedPromptFingerprint", "failedInputFingerprint", "batchFailedCount"] as const) {
        Object.assign(metadata, { [key]: before[key] });
    }
    metadata.status ??= "idle";
    return { ...node, metadata };
}
