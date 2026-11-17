import { describe, expect, test } from "bun:test";

import { sameProjectSettingsDraft, shouldApplyProjectSettingsMutation } from "@/pages/projects/detail/project-settings-session";


describe("project settings mutation identity", () => {
    test("草稿仅在全部已编辑字段与保存结果一致时变为干净", () => {
        const saved = { name: "项目甲", description: "", aspectRatio: "9:16", sourceType: "blank", stylePresetId: "" };
        expect(sameProjectSettingsDraft({ ...saved, defaultImageModel: "" }, saved)).toBe(true);
        expect(sameProjectSettingsDraft({ ...saved, description: "保存期间继续输入" }, saved)).toBe(false);
        expect(sameProjectSettingsDraft({ ...saved, defaultVideoModel: "另一模型" }, saved)).toBe(false);
    });
    test("same mounted project can apply success and ordinary errors", () => {
        expect(shouldApplyProjectSettingsMutation({
            mountedProjectId: "project-a",
            liveProjectId: "project-a",
            mounted: true,
        })).toBe(true);
    });

    test("project change does not apply a stale save or toast", () => {
        expect(shouldApplyProjectSettingsMutation({
            mountedProjectId: "project-a",
            liveProjectId: "project-b",
            mounted: true,
        })).toBe(false);
    });

    test("unmounted session does not toast", () => {
        expect(shouldApplyProjectSettingsMutation({
            mountedProjectId: "project-a",
            liveProjectId: "project-a",
            mounted: false,
        })).toBe(false);
    });
});
