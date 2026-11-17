import { afterAll, beforeAll, beforeEach, expect, test } from "bun:test";
import type { BunPlugin } from "bun";
import { spawn } from "node:child_process";

let server: ReturnType<typeof Bun.serve>;
let failSave = false;
let gatePatch = false;
let patchHits = 0;
let patchGate: { promise: Promise<void>; resolve: () => void } | undefined;
let patches: Array<{ projectId: string; body: Record<string, unknown> }> = [];
const projects = new Map<string, Record<string, unknown>>();

function resetPatchGate() {
    let resolve!: () => void;
    const promise = new Promise<void>((res) => {
        resolve = res;
    });
    patchGate = { promise, resolve };
}

function makeProject(id: string, status: string) {
    return {
        id,
        userId: "owner-a",
        name: id === "project-a" ? "项目甲" : "项目乙",
        type: "short-drama",
        aspectRatio: "9:16",
        sourceType: "blank",
        description: "",
        stylePresetId: "",
        status,
        revision: 1,
        createdAt: "2026-10-02T00:00:00.000Z",
        updatedAt: "2026-10-02T00:00:00.000Z",
    };
}

function seedProjects() {
    projects.clear();
    projects.set("project-a", makeProject("project-a", "active"));
    projects.set("project-b", makeProject("project-b", "active"));
}

function json(data: unknown, msg = "ok", code = 0) {
    return Response.json({ code, data, msg });
}

function projectIdFromPath(path: string) {
    const match = path.match(/^\/(?:api\/)?projects\/([^/]+)$/);
    return match?.[1] ? decodeURIComponent(match[1]) : "";
}

beforeAll(async () => {
    const stubs: Record<string, string> = {
        "@/lib/workspace-route-modules": "export function preloadWorkspaceRoute() {}",
        "@/lib/plugins/builtin": "export {}",
        "@/lib/plugins/plugin-client-loader": "export function syncActiveClientPlugins() {}",
        "@/components/layout/workspace-sidebar-account": "export function WorkspaceSidebarAccount() { return null; }",
        "@/components/layout/workspace-sidebar-update": "export function WorkspaceSidebarUpdate() { return null; }",
        "@/components/layout/workspace-sidebar-social-links": "export function WorkspaceSidebarSocialLinks() { return null; }",
        "@/components/onboarding/first-creation-guide": "export function OnboardingEntry() { return null; }",
    };
    const plugins: BunPlugin[] = [
        {
            name: "test-source-alias",
            setup(builder) {
                builder.onResolve({ filter: /^@\// }, (args) => args.path in stubs ? { path: args.path, namespace: "stub" } : ({ path: Bun.resolveSync("../src/" + args.path.slice(2), import.meta.dir) }));
                builder.onLoad({ filter: /.*/, namespace: "stub" }, (args) => ({ contents: stubs[args.path], loader: "ts" }));
                builder.onLoad({ filter: /\.(css|svg|png|jpe?g|gif|webp|woff2?)$/ }, () => ({
                    contents: "export default ''",
                    loader: "js",
                }));
            },
        },
    ];
    const build = await Bun.build({
        entrypoints: [import.meta.dir + "/fixtures/project-settings-view-harness.tsx"],
        plugins,
        target: "browser",
        define: {
            "import.meta.env.DEV": "true",
            "import.meta.env.PROD": "false",
            "import.meta.env.MODE": '"development"',
            "import.meta.env.VITE_CANVAS_LOCAL_MODE": '"false"',
            "import.meta.env.VITE_CANVAS_BACKEND_URL": '""',
            "process.env.NODE_ENV": '"development"',
        },
    });
    if (!build.success) throw new Error(build.logs.join("\n"));
    const script = await build.outputs[0].text();
    server = Bun.serve({
        port: 0,
        async fetch(request) {
            const url = new URL(request.url);
            const path = url.pathname;
            if (path === "/harness.js") return new Response(script, { headers: { "Content-Type": "text/javascript" } });
            if (path === "/harness/release-patch" && request.method === "POST") {
                patchGate?.resolve();
                return Response.json({ ok: true });
            }
            if (path === "/harness/patches") {
                return Response.json({ patches, patchHits });
            }
            if (path === "/api/project-folders") return json({ folders: [] });
            if (path === "/api/projects" && request.method === "GET") return json({ projects: [], page: 1, pageSize: 50, total: 0, hasMore: false });
            if (path === "/api/projects" && request.method === "POST") {
                const body = await request.json() as Record<string, unknown>;
                const project = { ...makeProject("new-project", "active"), ...body };
                projects.set("new-project", project);
                patches.push({ projectId: "new-project", body });
                return json({ project });
            }
            if (path === "/api/projects/project-a/units/unit-a" && request.method === "PATCH") {
                const body = await request.json() as Record<string, unknown>;
                patchHits += 1;
                patches.push({ projectId: "project-a", body });
                if (gatePatch && patchGate) await patchGate.promise;
                const current = projects.get("project-a")!;
                if (body.expectedRevision !== current.revision) return Response.json({ code: 409, reason: "revision_conflict", msg: "章节已被其他操作更新，草稿已保留" }, { status: 409 });
                const revision = Number(current.revision) + 1;
                projects.set("project-a", { ...current, revision });
                return json({ unit: { id: "unit-a", projectId: "project-a", title: body.title, sourceText: body.sourceText, status: "ready", wordCount: 10, updatedAt: new Date().toISOString() }, projectRevision: revision });
            }
            const projectId = projectIdFromPath(path);
            if (projectId && request.method === "GET") {
                const project = projects.get(projectId) || makeProject(projectId, "active");
                return json({ project });
            }
            if (projectId && request.method === "PATCH") {
                const body = await request.json() as Record<string, unknown>;
                patchHits += 1;
                patches.push({ projectId, body });
                if (gatePatch && patchGate) await patchGate.promise;
                if (failSave) return json(null, "保存失败：网络中断", 1);
                const current = projects.get(projectId) || makeProject(projectId, "active");
                if (body.expectedRevision !== current.revision) return Response.json({ code: 409, reason: "revision_conflict", msg: "项目已被其他操作更新，草稿已保留" }, { status: 409 });
                const next = { ...current, ...body, id: projectId, revision: Number(current.revision) + 1 };
                projects.set(projectId, next);
                return json({ project: next });
            }
            if (path.startsWith("/api/")) return json([]);
            return new Response('<meta name="viewport" content="width=device-width, initial-scale=1"><script>if (!("Bun" in globalThis)) globalThis.Bun = {};</script><div id="root"></div><script type="module" src="/harness.js"></script>', { headers: { "Content-Type": "text/html" } });
        },
    });
}, 60_000);

beforeEach(() => {
    failSave = false;
    gatePatch = false;
    patchHits = 0;
    patches = [];
    seedProjects();
    resetPatchGate();
});

afterAll(() => { server?.stop(true); });

async function runScenario(scenario: string) {
    const output = await new Promise<string>((resolve, reject) => {
        // Node 执行完整浏览器场景，避免 Bun 在 Windows 上的调试管道握手阻塞。
        const child = spawn("node", [import.meta.dir + "/fixtures/project-settings-browser-runner.mjs", server.url.toString(), scenario], { cwd: import.meta.dir + "/..", windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
        let output = "";
        child.stdout.on("data", (chunk) => { output += String(chunk); });
        child.stderr.on("data", (chunk) => { output += String(chunk); });
        child.once("error", reject);
        child.once("close", (code) => code === 0 ? resolve(output) : reject(new Error(output || `浏览器场景退出：${code}`)));
    });
    expect(output).not.toContain("AssertionError");
}

test("同一挂载项目可归档后恢复", () => runScenario("archive"), 20_000);
test("StrictMode 重挂载仍能保存有效草稿", () => runScenario("strict"), 20_000);
test("项目 ABA 切换后迟到成功不写入新组件", () => { gatePatch = true; return runScenario("stale-success"); }, 20_000);
test("项目 ABA 切换后迟到错误不提示新组件", () => { gatePatch = true; failSave = true; return runScenario("stale-error"); }, 20_000);
test("后台刷新后旧草稿仍按原版本拒绝覆盖", () => runScenario("conflict"), 20_000);
test("平台退出刷新保存后立即报告草稿干净", () => runScenario("draft-flush"), 20_000);
test("平台退出同步的版本冲突保留草稿并继续阻止退出", () => runScenario("draft-conflict"), 20_000);
test("平台退出复用在途保存并保留保存期间的新增输入", () => { gatePatch = true; return runScenario("draft-pending"); }, 20_000);
test("章节退出同步成功后立即报告干净草稿", () => runScenario("chapter-flush"), 20_000);
test("章节退出版本冲突保留正文与编辑基准", () => runScenario("chapter-conflict"), 20_000);
test("章节退出复用在途保存并保留新增正文", () => { gatePatch = true; return runScenario("chapter-pending"); }, 20_000);
test("平台导航区分项目画布素材并通过项目页创建真实项目", () => runScenario("library"), 25_000);
