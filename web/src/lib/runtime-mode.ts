import { useUserStore } from "@/stores/use-user-store";
import { apiBaseURL } from "@/services/api/request";
import { isDesktopShellOrigin } from "@/lib/desktop-shell";

export type RuntimeSessionLike = {
    runtimeMode?: "unknown" | "local" | "platform";
};

/** 运行模式以服务端 `/runtime` 合同为准；未知时不开放本地能力。 */
export function isLocalRuntimeModeForSession(session: RuntimeSessionLike) {
    return session.runtimeMode === "local";
}

export function isLocalRuntimeMode() {
    return isLocalRuntimeModeForSession(useUserStore.getState());
}

/** True inside the native Wails desktop shell or when targeting the loopback Go API. */
export function isNativeDesktopRuntime() {
    return isDesktopShellOrigin()
        || /^http:\/\/127\.0\.0\.1:\d+\/api$/u.test(String(apiBaseURL));
}
