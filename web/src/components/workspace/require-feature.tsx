import type { ReactNode } from "react";
import { Button } from "antd";
import { useNavigate } from "react-router";

import { WorkspacePage } from "@/components/layout/workspace-page";
import { WorkspaceState } from "@/components/layout/workspace-state";
import { useUserStore } from "@/stores/use-user-store";
import { isWorkspaceFeatureEnabled, type WorkspaceFeature } from "@/lib/workspace-features";
import { isLocalWorkspaceMode } from "@/services/workspace-mode";

const featureNames: Record<WorkspaceFeature, string> = {
    shortDramaEnabled: "短剧创作",
    taskCenterEnabled: "任务中心",
    frontendModelsEnabled: "前台模型",
    pluginCenterEnabled: "插件",
    customChannelsEnabled: "模型配置",
    assistantEnabled: "创作助手",
    proposalEnabled: "提案生产",
    pluginsEnabled: "插件",
    skillsEnabled: "技能",
    eagleEnabled: "Eagle 素材库",
    localAgentEnabled: "本机连接",
    creationEnabled: "创作",
    generationEnabled: "生成",
};

export function RequireFeature({ feature, children }: { feature: WorkspaceFeature; children: ReactNode }) {
    const navigate = useNavigate();
    const features = useUserStore((state) => state.features);
    const localMode = isLocalWorkspaceMode();
    if (!isWorkspaceFeatureEnabled(feature, features, localMode)) {
        return (
            <WorkspacePage>
                <WorkspaceState icon="empty" title={!localMode && feature === "generationEnabled" ? "平台生成尚未开放" : `${featureNames[feature]}暂不可用`} description={localMode ? "当前本地工作区未启用此模块。" : "当前平台未开放此功能。"} action={<Button type="primary" onClick={() => navigate("/", { replace: true })}>返回工作区</Button>} />
            </WorkspacePage>
        );
    }
    return children;
}
