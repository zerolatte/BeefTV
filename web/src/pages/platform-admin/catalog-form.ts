import { useRef, useState } from "react";
import { captureWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";
import { useUserStore } from "@/stores/use-user-store";

export function usePlatformAdminKey() {
    const accountId = useUserStore((state) => state.user?.id);
    const instanceId = useUserStore((state) => state.platformInstanceId);
    return ["platform-admin", instanceId, accountId] as const;
}

/** 表单验证和请求共用单次提交锁，迟到响应不能修改另一个会话的页面。 */
export function useCatalogSubmit() {
    const saving = useRef(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string>();
    const submit = async (save: () => Promise<unknown>, done: () => void) => {
        if (saving.current) return;
        const session = captureWorkspaceSession();
        saving.current = true;
        setBusy(true);
        setError(undefined);
        try {
            await save();
            if (isWorkspaceSessionCurrent(session)) done();
        } catch (failure) {
            if (isWorkspaceSessionCurrent(session) && !(failure && typeof failure === "object" && "errorFields" in failure)) {
                setError(failure instanceof Error ? failure.message : "保存失败，请重试。");
            }
        } finally {
            saving.current = false;
            setBusy(false);
        }
    };
    return { busy, error, submit };
}

/** 同一表单的失败重试保留请求正文与标识，只有实际提交内容变化才建立新意图。 */
export function useAdminRequestIntent<T extends object>() {
    const intent = useRef<{ fingerprint: string; payload: T & { clientOperationId: string } } | undefined>(undefined);
    return (input: T) => {
        const fingerprint = JSON.stringify(input);
        if (!intent.current || intent.current.fingerprint !== fingerprint) {
            intent.current = { fingerprint, payload: { ...input, clientOperationId: crypto.randomUUID() } };
        }
        return intent.current.payload;
    };
}

export function parseJSONField<T>(raw: string | undefined, kind: "object" | "array", label: string): T {
    try {
        const value: unknown = JSON.parse(raw || (kind === "array" ? "[]" : "{}"));
        if (kind === "array" ? !Array.isArray(value) : !value || typeof value !== "object" || Array.isArray(value)) throw new Error();
        return value as T;
    } catch { throw new Error(`${label}必须是有效的 JSON ${kind === "array" ? "数组" : "对象"}`); }
}

export const capabilityNames: Record<string, string> = { text: "文本", image: "图片", video: "视频", audio: "音频" };
