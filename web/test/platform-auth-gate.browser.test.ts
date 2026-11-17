import { afterAll, beforeAll, expect, test } from "bun:test";
import { spawn } from "node:child_process";

let server: ReturnType<typeof Bun.serve>;
const sessions = new Map<string, string>();
const changedPasswords = new Set<string>();
const failedBootstraps = new Set<string>();
const checks = new Map<string, number>();

function account(username: string) {
    return { id: `account-${username}`, workspaceId: `workspace-${username}`, username, displayName: username, role: "user", status: "active", mustChangePassword: username === "new-user" && !changedPasswords.has(username) };
}

function grant(username: string, sessionId: string) {
    return { account: account(username), sessionId, csrfToken: "csrf-test-token", expiresAt: "2099-01-01T00:00:00Z" };
}

function json(data: unknown, status = 200, reason = "") {
    return Response.json({ code: status === 200 ? 0 : status, data, msg: status === 200 ? "ok" : "登录状态已失效", reason }, { status });
}

beforeAll(async () => {
    const stubs: Record<string, string> = {
        "@/lib/user-session": `import { useUserStore } from "@/stores/use-user-store";
            export async function applyPlatformUserSession(payload) {
                window.platformTest.hydrations += 1;
                useUserStore.getState().setUser(payload.account);
                useUserStore.getState().setHydrated(true);
                useUserStore.getState().setAuthState("authenticated");
            }`,
        "@/services/platform-workspace-cleanup": `export function activatePlatformWorkspaceCacheScope() {}
            export async function inspectPlatformDrafts() { return {hasUnsynced: Boolean(window.platformTest.draft), drafts: window.platformTest.draft ? [{ id: "draft" }] : []}; }
            export async function flushPlatformDrafts() { window.platformTest.flushes += 1; if (window.platformTest.failFlush) throw new Error("草稿尚未同步，请继续同步或明确放弃。"); }
            export async function clearPlatformWorkspaceCache() { if (window.platformTest.failClear) throw new Error("缓存清理失败，暂时不能切换账号。"); window.platformTest.clears += 1; }`,
    };
    const build = await Bun.build({
        entrypoints: [import.meta.dir + "/fixtures/platform-auth-gate-harness.tsx"],
        target: "browser",
        define: { "import.meta.env": "{}", "process.env.NODE_ENV": '"development"' },
        plugins: [{
            name: "platform-auth-test-alias",
            setup(builder) {
                builder.onResolve({ filter: /^@\// }, (args) => args.path in stubs ? { path: args.path, namespace: "stub" } : { path: Bun.resolveSync("../src/" + args.path.slice(2), import.meta.dir) });
                builder.onLoad({ filter: /.*/, namespace: "stub" }, (args) => ({ contents: stubs[args.path], loader: "ts", resolveDir: import.meta.dir }));
                builder.onLoad({ filter: /\.(css|svg|png)$/ }, () => ({ contents: "export default ''", loader: "js" }));
            },
        }],
    });
    if (!build.success) throw new Error(build.logs.join("\n"));
    const script = await build.outputs[0].text();
    server = Bun.serve({
        port: 0,
        async fetch(request) {
            const url = new URL(request.url);
            if (url.pathname === "/test.js") return new Response(script, { headers: { "Content-Type": "text/javascript" } });
            const sessionId = /test_session=([^;]+)/.exec(request.headers.get("cookie") || "")?.[1] || "";
            const username = sessions.get(sessionId);
            if (url.pathname === "/test/expire") {
                sessions.delete(sessionId);
                return json({ expired: true });
            }
            if (url.pathname === "/test/auth-checks") return json({ count: checks.get(sessionId) || 0 });
            if (url.pathname === "/api/auth/csrf") return json({ csrfToken: "csrf-test-token" });
            if (url.pathname === "/api/auth/login") {
                const body = await request.json() as { username: string; password: string };
                if (body.password !== "password-long-enough") return json(null, 401, "login_failed");
                const nextSession = crypto.randomUUID();
                sessions.set(nextSession, body.username);
                const response = json(grant(body.username, nextSession));
                response.headers.set("Set-Cookie", `test_session=${nextSession}; Path=/; HttpOnly; SameSite=Lax`);
                return response;
            }
            if (url.pathname.startsWith("/api/") && !username) return json(null, 401, "unauthenticated");
            if (url.pathname === "/api/auth/me") {
                checks.set(sessionId, (checks.get(sessionId) || 0) + 1);
                return json(grant(username!, sessionId));
            }
            if (url.pathname === "/api/auth/password") {
                changedPasswords.add(username!);
                const nextSession = crypto.randomUUID();
                sessions.set(nextSession, username!);
                const response = json(grant(username!, nextSession));
                response.headers.set("Set-Cookie", `test_session=${nextSession}; Path=/; HttpOnly; SameSite=Lax`);
                return response;
            }
            if (url.pathname === "/api/auth/logout") {
                sessions.delete(sessionId);
                return json({ loggedOut: true });
            }
            if (url.pathname === "/api/workspace/bootstrap") {
                if (username === "partial" && !failedBootstraps.has(username)) {
                    failedBootstraps.add(username);
                    return json(null, 503, "bootstrap_failed");
                }
                return json({ contractVersion: 1, instanceId: "test-platform", profile: "platform", account: account(username!), sessionId, workspace: { id: `workspace-${username}`, name: username, owner: "platform", storage: "sqlite" }, capabilities: { localAssets: false, providerCalls: false }, features: { shortDrama: true, assistant: false, plugins: false, skills: false, generation: false } });
            }
            return new Response('<!doctype html><html lang="zh-CN"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><div id="root"></div><script type="module" src="/test.js"></script></body></html>', { headers: { "Content-Type": "text/html" } });
        },
    });
}, 30_000);

afterAll(async () => {
    server?.stop();
});

async function runScenario(scenario: string) {
    // Node 驱动浏览器可避开 Bun 在 Windows 上的远程调试管道兼容问题。
    const output = await new Promise<string>((resolve, reject) => {
        const child = spawn("node", [import.meta.dir + "/fixtures/platform-auth-browser-runner.mjs", server.url.toString(), scenario], { cwd: import.meta.dir + "/..", windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
        let text = "";
        child.stdout.on("data", (chunk) => { text += String(chunk); });
        child.stderr.on("data", (chunk) => { text += String(chunk); });
        child.once("error", reject);
        child.once("close", (code) => code === 0 ? resolve(text) : reject(new Error(text || `浏览器用例退出：${code}`)));
    });
    expect(output).not.toContain("AssertionError");
}

test("平台匿名与强制改密阶段不挂载业务工作区", () => runScenario("password"), 20_000);
test("会话失效保持原组件与草稿，原账号认证只同步而不重新水合", () => runScenario("recovery"), 20_000);
test("退出同步失败保留工作区，明确放弃且清理完成后才显示登录", () => runScenario("logout"), 20_000);
test("另一标签退出会立即遮挡旧工作区并保留原草稿", () => runScenario("cross-tab"), 20_000);
test("未完成引导会锁定原账号，明确放弃后才能进入另一账号", () => runScenario("partial-bootstrap"), 20_000);
test("可见页面主动检测服务端撤销，隐藏时停止校验并保留草稿", () => runScenario("session-monitor"), 20_000);
