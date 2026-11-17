import { productCss } from "./product-css";

/** 管理页行为测试共享真实脚本和样式构建，测试本身只断言浏览器行为。 */
export async function buildPlatformAdminBrowserAssets() {
    const build = await Bun.build({
        entrypoints: [import.meta.dir + "/platform-admin-harness.tsx"], target: "browser",
        define: { "import.meta.env": "{}", "process.env.NODE_ENV": '"development"' },
        plugins: [{ name: "platform-admin-browser-assets", setup(builder) {
            builder.onResolve({ filter: /^@\// }, (args) => ({ path: Bun.resolveSync("../../src/" + args.path.slice(2), import.meta.dir) }));
            // Bun 夹具和产品 Vite 保持相同的图标 ESM 输入。
            builder.onResolve({ filter: /^@ant-design\/icons-svg\/lib\// }, (args) => ({ path: Bun.resolveSync(args.path.replace("/lib/", "/es/") + ".js", import.meta.dir) }));
            builder.onLoad({ filter: /\.(css|svg|png)$/ }, () => ({ contents: "export default ''", loader: "js" }));
        } }],
    });
    if (!build.success) throw new Error(build.logs.join("\n"));
    return {
        script: await build.outputs[0].text(),
        styles: await productCss() + await Bun.file(import.meta.dir + "/../../src/pages/platform-admin/platform-admin.css").text(),
    };
}
