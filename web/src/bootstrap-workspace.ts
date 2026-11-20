import { bootstrapDesktopRuntime } from "@/services/desktop-runtime";
import { appPathname } from "@/lib/app-routing";
import { isEmbeddedRuntime } from "@/lib/embedded-runtime";
import { ApiError, enablePlatformRequests, bindPlatformUser, abortPlatformRequests } from "@/services/api/request";
import { getPlatformSession } from "@/services/api/platform";
import { setWorkspaceCapabilitySnapshot } from "@/services/workspace-mode";
import { setWorkspaceStorageScope } from "@/lib/user-scope";
import { getWorkspaceBootstrap, type WorkspaceBootstrapPayload } from "@/services/api/workspace";
import {
    beginStartupDiagnosticsScope,
    initializeClientDiagnostics,
    promoteStartupDiagnosticsScope,
} from "@/services/diagnostics/client-diagnostics";
import {
    clearWorkspaceStartupFailure,
    getDesktopBootSnapshot,
    isIsolatedDevReproPath,
    recordWorkspaceStartupFailure,
    setPrimedWorkspaceBootstrap,
    subscribeDesktopBootSnapshot,
} from "@/services/workspace-startup-state";

export async function ensureWorkspaceStorageHydrated(options?: { retry?: boolean }): Promise<WorkspaceBootstrapPayload> {
    await bootstrapDesktopRuntime(options?.retry ? { retry: true } : undefined);
    const payload = await getWorkspaceBootstrap();
    // Store modules automatically hydrate on import. Select this document's
    // durable workspace before they can read another window's last scope.
    setWorkspaceStorageScope(payload.workspace.id);
    promoteStartupDiagnosticsScope(payload.user?.id || payload.workspace.id);
    const [{ useCanvasStore }, { hydrateLocalCanvasProjectsFromBackend }] = await Promise.all([
        import("@/stores/canvas/use-canvas-store"),
        import("@/services/local-workspace-repository"),
    ]);
    // 浏览器缓存晚于后端读入完成时，会用旧连接数组盖掉刚合并的画布。
    await useCanvasStore.persist.rehydrate();
    if (!isEmbeddedRuntime()) await hydrateLocalCanvasProjectsFromBackend();
    clearWorkspaceStartupFailure();
    return payload;
}

async function mountWorkspaceApplication() {
    if (isEmbeddedRuntime()) {
        const { bootstrapAppearance } = await import("@/services/appearance-bootstrap");
        await bootstrapAppearance().catch(() => console.warn("外观初始化未完成，继续打开工作区"));
    }
    await import("./application");
}

export async function startApplication() {
    if (!isEmbeddedRuntime()) {
        enablePlatformRequests();
        const sessionChannel = new BroadcastChannel("beeftv-platform-session");
        sessionChannel.onmessage = () => { abortPlatformRequests(); window.location.reload(); };
        try {
            const session = await getPlatformSession();
            bindPlatformUser(session.user.id);
            if (session.user.mustChangePassword) { const { mountPlatformLogin } = await import("@/pages/platform/login"); mountPlatformLogin(true); return; }
            setWorkspaceCapabilitySnapshot({ contractVersion: 1, profile: "platform", capabilities: { localAssets: true, providerCalls: true } });
        } catch (error) {
            if (error instanceof ApiError && error.status === 401) { const { mountPlatformLogin } = await import("@/pages/platform/login"); mountPlatformLogin(); return; }
            const { mountStandaloneStartupError } = await import("./mount-startup-error");
            mountStandaloneStartupError({ initialFailure: recordWorkspaceStartupFailure(error), onRetryBootstrap: async () => { window.location.reload(); } });
            return;
        }
    }
    setPrimedWorkspaceBootstrap(null);
    beginStartupDiagnosticsScope();
    initializeClientDiagnostics();

    if (Boolean(import.meta.env?.DEV) && typeof window !== "undefined" && isIsolatedDevReproPath(appPathname())) {
        await mountWorkspaceApplication();
        return;
    }

    let settled = false;
    let progressMounted = false;
    let stopBootProgress = () => {};
    if (!isEmbeddedRuntime() && typeof document !== "undefined" && document.getElementById("root")) {
        const maybeShowProgress = () => {
            if (settled || progressMounted || getDesktopBootSnapshot().state !== "starting") return;
            progressMounted = true;
            void import("./mount-startup-error").then(({ mountStandaloneStartupProgress }) => {
                if (!settled) mountStandaloneStartupProgress();
            });
        };
        stopBootProgress = subscribeDesktopBootSnapshot(maybeShowProgress);
        maybeShowProgress();
    }

    try {
        const payload = await ensureWorkspaceStorageHydrated();
        settled = true;
        stopBootProgress();
        if (progressMounted) {
            const { unmountStandaloneStartupError } = await import("./mount-startup-error");
            unmountStandaloneStartupError();
        }
        setPrimedWorkspaceBootstrap(payload);
    } catch (error) {
        settled = true;
        stopBootProgress();
        if (isEmbeddedRuntime()) throw error;
        const failure = recordWorkspaceStartupFailure(error);
        if (typeof document !== "undefined" && document.getElementById("root")) {
            const { mountStandaloneStartupError, unmountStandaloneStartupError } = await import("./mount-startup-error");
            mountStandaloneStartupError({
                initialFailure: failure,
                onRetryBootstrap: async () => {
                    const payload = await ensureWorkspaceStorageHydrated({ retry: true });
                    setPrimedWorkspaceBootstrap(payload);
                    unmountStandaloneStartupError();
                    await mountWorkspaceApplication();
                },
            });
        }
        return;
    }

    await mountWorkspaceApplication();
}
