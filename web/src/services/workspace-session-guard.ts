import { getActiveUserScope } from "@/lib/user-scope";
import { captureWorkspaceSession, isWorkspaceSessionCurrent } from "@/services/workspace-session";

/** 队列与本地存储跨越 await 后，也必须保留开始时的会话和工作区。 */
export function captureWorkspaceGuard(scope = getActiveUserScope()) {
    const session = captureWorkspaceSession();
    return () => {
        if (!isWorkspaceSessionCurrent(session) || scope !== getActiveUserScope()) throw new DOMException("工作区会话已变更，修改保留在原工作区", "AbortError");
    };
}
