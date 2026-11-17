import { useState } from "react";
import { Navigate } from "react-router";
import { Button } from "antd";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { useUserStore } from "@/stores/use-user-store";
import { PlatformAdminUsers } from "./users";
import { PlatformAdminAudit } from "./audit";
import { PlatformAdminCatalog } from "./catalog";
import { PlatformAdminUsage } from "./usage";
import "./platform-admin.css";

export default function PlatformAdminPage() {
    const runtimeMode = useUserStore((state) => state.runtimeMode);
    const user = useUserStore((state) => state.user);
    const [tab, setTab] = useState<"users" | "audit" | "channels" | "models" | "usage">("users");
    if (runtimeMode !== "platform" || user?.role !== "admin") return <Navigate to="/" replace />;
    return (
        <WorkspacePage className="platform-admin-page">
            <nav className="platform-admin-tabs" aria-label="平台管理分区">
                <Button aria-pressed={tab === "users"} onClick={() => setTab("users")}>用户账号</Button>
                <Button aria-pressed={tab === "audit"} onClick={() => setTab("audit")}>管理审计</Button>
                <Button aria-pressed={tab === "channels"} onClick={() => setTab("channels")}>平台渠道</Button>
                <Button aria-pressed={tab === "models"} onClick={() => setTab("models")}>逻辑模型</Button>
                <Button aria-pressed={tab === "usage"} onClick={() => setTab("usage")}>用量记录</Button>
            </nav>
            {tab === "users" ? <PlatformAdminUsers /> : tab === "audit" ? <PlatformAdminAudit /> : tab === "usage" ? <PlatformAdminUsage /> : <PlatformAdminCatalog tab={tab} />}
        </WorkspacePage>
    );
}
