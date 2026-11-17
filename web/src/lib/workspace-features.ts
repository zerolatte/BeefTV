import type { FeatureAvailability } from "@/stores/use-user-store";

export type WorkspaceFeature = "shortDramaEnabled" | "taskCenterEnabled" | "frontendModelsEnabled" | "pluginCenterEnabled" | "customChannelsEnabled" | "assistantEnabled" | "proposalEnabled" | "pluginsEnabled" | "skillsEnabled" | "eagleEnabled" | "localAgentEnabled" | "creationEnabled" | "generationEnabled";

const localOnlyFeatures = new Set<WorkspaceFeature>([
    "customChannelsEnabled", "pluginCenterEnabled", "pluginsEnabled", "skillsEnabled", "eagleEnabled", "localAgentEnabled", "assistantEnabled", "proposalEnabled", "creationEnabled",
]);

/** 平台入口与导航共用运行模式和服务端功能合同，不从用户名或缓存推断权限。 */
export function isWorkspaceFeatureEnabled(feature: WorkspaceFeature, features: FeatureAvailability, localMode: boolean): boolean {
    if (!localMode && localOnlyFeatures.has(feature)) return false;
    if (feature === "generationEnabled" && !localMode) return features.generationEnabled === true;
    return features[feature] !== false;
}
