import type { Project } from "@/services/api/projects";

const settingFields = ["name", "description", "aspectRatio", "sourceType", "stylePresetId", "styleProfileJson", "defaultImageModel", "defaultVideoModel"] as const;
export type ProjectSettingsDraft = Pick<Project, typeof settingFields[number]>;

export function sameProjectSettingsDraft(left: ProjectSettingsDraft, right: ProjectSettingsDraft) {
    return settingFields.every((field) => (left[field] || "") === (right[field] || ""));
}

export function shouldApplyProjectSettingsMutation(input: {
    mountedProjectId: string;
    liveProjectId: string;
    mounted: boolean;
}) {
    if (!input.mounted) return false;
    return input.mountedProjectId === input.liveProjectId;
}
