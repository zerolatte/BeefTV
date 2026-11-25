export function isSpatialWorkbenchPath(pathname: string) {
    // 画布外页面统一使用工作区布局，画布编辑器保留独立布局。
    const canvasEditor = /^\/canvas\/[^/]+(?:\/|$)/.test(pathname);
    return !canvasEditor;
}
