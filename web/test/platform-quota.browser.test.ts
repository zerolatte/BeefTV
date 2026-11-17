import { afterAll, beforeAll, expect, test } from "bun:test";
import { spawn } from "node:child_process";
import { buildPlatformAdminBrowserAssets } from "./fixtures/platform-admin-browser-assets";
import { quotaFixture } from "./fixtures/platform-quota-api";

let script: string;
let styles: string;
const servers: ReturnType<typeof Bun.serve>[] = [];
beforeAll(async () => {
    ({ script, styles } = await buildPlatformAdminBrowserAssets());
}, 30_000);
afterAll(() => servers.forEach((server) => server.stop()));

async function scenario(name: string) {
    const api = quotaFixture();
    const server = Bun.serve({ port: 0, async fetch(request) {
        if (new URL(request.url).pathname === "/test.js") return new Response(script, { headers: { "Content-Type": "text/javascript" } });
        const response = await api.handle(request);
        if (response) return response;
        return new Response(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>${styles}</style></head><body><div id="root"></div><script type="module" src="/test.js"></script></body></html>`, { headers: { "Content-Type": "text/html" } });
    } });
    servers.push(server);
    const output = await new Promise<string>((resolve, reject) => {
        const child = spawn("node", [import.meta.dir + "/fixtures/platform-quota-browser-runner.mjs", server.url.toString(), name], { cwd: import.meta.dir + "/..", windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
        let output = "";
        child.stdout.on("data", (chunk) => { output += String(chunk); });
        child.stderr.on("data", (chunk) => { output += String(chunk); });
        child.once("error", reject);
        child.once("close", (code) => code === 0 ? resolve(output) : reject(new Error(output || `浏览器退出：${code}`)));
    });
    expect(output).not.toContain("AssertionError");
    server.stop();
}

test("普通账号不能读取点数管理或用量", () => scenario("guard"), 40_000);
test("点数调整必填原因并拒绝零值、小数和超范围整数", () => scenario("validation"), 40_000);
test("调整已记账但响应断开时复用幂等标识且只记账一次", () => scenario("retry"), 40_000);
test("点数扣减失败保留草稿且修改内容使用新标识", () => scenario("negative"), 40_000);
test("用量按服务端分页筛选并展示状态和核对标记", () => scenario("usage"), 40_000);
test("人工核对要求终局依据且丢失响应后同键重试", () => scenario("review"), 40_000);
test("人工核对状态冲突保留原状态和表单草稿", () => scenario("review-conflict"), 40_000);
test("账本核对按需读取且大整数原样显示", () => scenario("reconciliation"), 40_000);
