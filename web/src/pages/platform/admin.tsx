import { useState } from "react";
import { Tabs } from "antd";
import { PageHeader } from "@/components/layout/workspace-page";
import { AccountAdmin } from "./accounts";
import { ModelAdmin } from "./models";
import { UsageAdmin } from "./usage";
import "./platform.css";

export default function PlatformAdminPage() {
    const [tab, setTab] = useState("accounts");
    return <main className="platform-page"><PageHeader title="平台管理" /><div className="platform-content platform-admin-content">
        <Tabs className="platform-tabs" activeKey={tab} onChange={setTab} items={[{ key: "accounts", label: "账号与积分" }, { key: "models", label: "模型与价格" }, { key: "usage", label: "用量与核对" }]} />
        {tab === "accounts" ? <AccountAdmin /> : tab === "models" ? <ModelAdmin /> : <UsageAdmin />}
    </div></main>;
}
