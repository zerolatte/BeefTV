import { ApiError } from "@/services/api/request";
import { getRuntimeMode } from "@/services/workspace-mode";
import { useUserStore } from "@/stores/use-user-store";

export const PLATFORM_GENERATION_DISABLED_MESSAGE = "平台生成尚未开放";

/** 平台真实生成在计点验收前关闭；任务、专项工具和客户端文本共用此门禁。 */
export function assertGenerationAvailable(): void {
    if (getRuntimeMode() === "platform" && useUserStore.getState().features.generationEnabled !== true) {
        throw new ApiError(PLATFORM_GENERATION_DISABLED_MESSAGE, { status: 403, reason: "generation_disabled", retryable: false });
    }
}

export function assertClientTextExecutionAvailable(): void {
    if (getRuntimeMode() === "platform") {
        throw new ApiError("平台文本任务由服务端执行", { status: 403, reason: "client_text_execution_disabled", retryable: false });
    }
}
