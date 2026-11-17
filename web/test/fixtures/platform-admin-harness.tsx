import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App, ConfigProvider } from "antd";
import { MemoryRouter, Route, Routes } from "react-router";

import PlatformAdminPage from "@/pages/platform-admin";
import PlatformModelCatalogPage from "@/pages/platform-model-catalog";
import { getAntThemeConfig, getWorkspaceAntThemeConfig } from "@/lib/app-theme";
import { applySkinTheme } from "@/lib/skin-themes";
import { setRuntimeMode } from "@/services/workspace-mode";
import { activateWorkspaceSession } from "@/services/workspace-session";
import { setPlatformCsrfToken } from "@/services/api/request";
import { useUserStore } from "@/stores/use-user-store";

const params = new URLSearchParams(location.search);
const role = params.get("role") === "user" ? "user" : "admin";
const dark = params.get("theme") === "dark";
document.documentElement.classList.toggle("dark", dark);
document.body.classList.add("app-user-overlays", "app-product-overlays");
applySkinTheme("classic", dark ? "dark" : "light");
setRuntimeMode("platform", "test-platform");
useUserStore.getState().setUser({ id: "admin-1", workspaceId: "workspace-admin", username: "manager", displayName: "平台管理员", role, status: "active" });
useUserStore.getState().setAuthState("authenticated");
useUserStore.getState().setFeatures({ generationEnabled: false });
activateWorkspaceSession({ mode: "platform", accountId: "admin-1", workspaceId: "workspace-admin", sessionId: "test-session" });
setPlatformCsrfToken("test-csrf");

createRoot(document.getElementById("root")!).render(
    <ConfigProvider theme={getAntThemeConfig(dark)}><ConfigProvider theme={getWorkspaceAntThemeConfig()}>
        <App><QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
            <div className="app-user-workspace app-product-workspace h-dvh overflow-hidden"><MemoryRouter initialEntries={[params.get("page") === "models" ? "/models" : "/admin"]}><Routes>
                <Route path="/admin" element={<PlatformAdminPage />} />
                <Route path="/models" element={<PlatformModelCatalogPage />} />
                <Route path="/" element={<p>创作工作区</p>} />
            </Routes></MemoryRouter></div>
        </QueryClientProvider></App>
    </ConfigProvider></ConfigProvider>,
);
