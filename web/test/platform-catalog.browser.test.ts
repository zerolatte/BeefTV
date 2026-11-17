import { afterAll, beforeAll, expect, test } from "bun:test";
import { spawn } from "node:child_process";
import { buildPlatformAdminBrowserAssets } from "./fixtures/platform-admin-browser-assets";
import { catalogFixture } from "./fixtures/platform-catalog-api";

let script: string;
let styles: string;
const servers: ReturnType<typeof Bun.serve>[] = [];
beforeAll(async () => {
    ({ script, styles } = await buildPlatformAdminBrowserAssets());
}, 30_000);
afterAll(() => servers.forEach((server) => server.stop()));

async function scenario(name: string) {
    const api = catalogFixture();
    const server = Bun.serve({ port: 0, async fetch(request) {
        if (new URL(request.url).pathname === "/test.js") return new Response(script, { headers: { "Content-Type": "text/javascript" } });
        const response = await api.handle(request);
        if (response) return response;
        return new Response(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>${styles}</style></head><body><div id="root"></div><script type="module" src="/test.js"></script></body></html>`, { headers: { "Content-Type": "text/html" } });
    } });
    servers.push(server);
    const result = await new Promise<string>((resolve, reject) => {
        const child = spawn("node", [import.meta.dir + "/fixtures/platform-catalog-browser-runner.mjs", server.url.toString(), name], { cwd: import.meta.dir + "/..", windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
        let output = "";
        child.stdout.on("data", (chunk) => { output += String(chunk); });
        child.stderr.on("data", (chunk) => { output += String(chunk); });
        child.once("error", reject);
        child.once("close", (code) => code === 0 ? resolve(output) : reject(new Error(output || `浏览器退出：${code}`)));
    });
    expect(result).not.toContain("AssertionError");
    server.stop();
}
test("普通用户只读取平台授权目录和固定点数", () => scenario("catalog"), 40_000);
test("管理员创建、启停渠道和轮换凭据", () => scenario("channels"), 40_000);
test("渠道模型只使用服务端允许协议并发送合同字段", () => scenario("channel-model"), 40_000);
test("模型新版本保留完整配置并在冲突时保留草稿", () => scenario("model"), 40_000);
test("固定点数价格拒绝零值并追加历史版本", () => scenario("prices"), 40_000);
test("模型授权跨页保留选择并可明确清空", () => scenario("grants"), 40_000);
