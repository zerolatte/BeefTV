import { afterAll, beforeAll, expect, test } from "bun:test";
import { spawn } from "node:child_process";
import { buildPlatformAdminBrowserAssets } from "./fixtures/platform-admin-browser-assets";

let server: ReturnType<typeof Bun.serve>;
const accounts = [
    { id: "admin-1", workspaceId: "workspace-admin", username: "manager", displayName: "平台管理员", role: "admin", status: "active", mustChangePassword: false, createdAt: "2026-01-01T00:00:00Z" },
    { id: "user-1", workspaceId: "workspace-user", username: "alice", displayName: "测试用户", role: "user", status: "active", mustChangePassword: false, createdAt: "2026-01-02T00:00:00Z" },
];
let limitBytes = 1073741824;
const writes: { path: string; method: string; body: Record<string, unknown> }[] = [];
let adminReads = 0;

function json(data: unknown, status = 200, msg = "ok") {
    return Response.json({ code: status === 200 ? 0 : status, data, msg }, { status });
}
function quota(id: string) {
    return { accountId: id, points: { granted: 30, settled: 5, reserved: 2, available: 23 }, storage: { limitBytes, usedBytes: 100, reservedBytes: 20 } };
}

beforeAll(async () => {
    const { script, styles } = await buildPlatformAdminBrowserAssets();
    server = Bun.serve({ port: 0, async fetch(request) {
        const url = new URL(request.url);
        if (url.pathname === "/test.js") return new Response(script, { headers: { "Content-Type": "text/javascript" } });
        if (url.pathname === "/test/state") return json({ writes, adminReads });
        if (url.pathname.startsWith("/api/admin/")) {
            if (request.method === "GET") adminReads += 1;
            const body = request.method === "GET" || request.method === "DELETE" ? {} : await request.json() as Record<string, unknown>;
            if (request.method !== "GET") {
                if (request.headers.get("x-csrf-token") !== "test-csrf" || request.headers.get("x-platform-session") !== "test-session") return json(null, 403, "认证请求头缺失");
                writes.push({ path: url.pathname, method: request.method, body });
            }
            if (url.pathname === "/api/admin/users" && request.method === "GET") return json({ users: accounts, total: accounts.length, page: 1, pageSize: 20 });
            if (url.pathname === "/api/admin/users" && request.method === "POST") {
                if (body.initialPoints === undefined || body.storageLimitBytes === undefined) return json(null, 400, "初始点数和存储上限必填");
                const account = { ...accounts[1], ...body, id: "created-user", workspaceId: "created-workspace", mustChangePassword: true };
                delete (account as Record<string, unknown>).password;
                accounts.push(account);
                return json({ user: account });
            }
            const match = /^\/api\/admin\/users\/([^/]+)(.*)$/.exec(url.pathname);
            if (match) {
                const account = accounts.find((entry) => entry.id === match[1]);
                if (!account) return json(null, 404, "账号不存在");
                if (match[2] === "/quota") return json(quota(account.id));
                if (match[2] === "/storage-quota") { limitBytes = Number(body.limitBytes); return json(quota(account.id)); }
                if (match[2] === "/reset-password") { account.mustChangePassword = true; return json({ reset: true }); }
                if (request.method === "PATCH") { Object.assign(account, body); return json({ user: account }); }
                if (request.method === "DELETE") { account.status = "deleted"; return json({ id: account.id, status: "deleted" }); }
            }
            if (url.pathname === "/api/admin/audit-logs") return json({ logs: [{ id: "audit-1", actorAccountId: "admin-1", targetAccountId: "user-1", action: "storage_limit_updated", difference: '{"storageLimitBytes":{"from":1073741824,"to":0}}', createdAt: "2026-01-02T00:00:00Z" }], total: 1, page: 1, pageSize: 20 });
            return json(null, 404);
        }
        return new Response(`<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><style>${styles}</style></head><body><div id="root"></div><script type="module" src="/test.js"></script></body></html>`, { headers: { "Content-Type": "text/html" } });
    } });
}, 30_000);

afterAll(() => server?.stop());

async function runScenario(scenario: string) {
    const output = await new Promise<string>((resolve, reject) => {
        const child = spawn("node", [import.meta.dir + "/fixtures/platform-admin-browser-runner.mjs", server.url.toString(), scenario], { cwd: import.meta.dir + "/..", windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
        let output = "";
        child.stdout.on("data", (chunk) => { output += String(chunk); });
        child.stderr.on("data", (chunk) => { output += String(chunk); });
        child.once("error", reject);
        child.once("close", (code) => code === 0 ? resolve(output) : reject(new Error(output || `浏览器用例退出：${code}`)));
    });
    expect(output).not.toContain("AssertionError");
}

test("普通用户不能挂载平台管理数据页", () => runScenario("guard"), 40_000);
test("管理开户必填额度且保存只发送合同字段", () => runScenario("create"), 40_000);
test("管理员可以修改账号、额度和密码并确认禁用注销", () => runScenario("manage"), 60_000);
test("管理审计展示脱敏差异并支持窄窗口", () => runScenario("audit"), 40_000);
