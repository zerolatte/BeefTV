import { afterEach, expect, test } from "bun:test";
import { useThemeStore } from "../src/stores/use-theme-store";
import { isSpatialWorkbenchPath } from "../src/lib/workspace-routes";

const before = useThemeStore.getState();
afterEach(() => useThemeStore.setState(before));

test("新用户和无效主题偏好使用浅色", () => {
    expect(useThemeStore.getInitialState().theme).toBe("light");
    useThemeStore.getState().setTheme("invalid");
    expect(useThemeStore.getState().preference).toBe("light");
});

test("用户仍可显式选择深色主题", () => {
    useThemeStore.getState().setTheme("dark");
    expect(useThemeStore.getState()).toMatchObject({ theme: "dark", preference: "dark" });
});

test("管理和列表页面共用工作区布局，画布编辑器独立", () => {
    for (const route of ["/", "/admin", "/credits", "/project", "/assets", "/canvas"]) expect(isSpatialWorkbenchPath(route)).toBe(true);
    expect(isSpatialWorkbenchPath("/canvas/project-id")).toBe(false);
});
