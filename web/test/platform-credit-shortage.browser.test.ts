import { afterAll, beforeAll, expect, test } from "bun:test";
import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { chromium, type Browser } from "playwright";
import { compileAppGlobalsStyles, readAppWorkspaceProductSource } from "./helpers/app-styles";

let browser: Browser;
let server: ReturnType<typeof Bun.serve>;
let rejection = true;
let balanceFailure = false;
let balanceGate: Promise<void> | undefined;
let calls: string[] = [];

beforeAll(async () => {
    const build = await Bun.build({ entrypoints: [import.meta.dir + "/fixtures/platform-credit-shortage-harness.tsx"], target: "browser", define: { "import.meta.env": "{}", "process.env.NODE_ENV": '"production"' }, plugins: [{ name: "source-alias", setup(builder) {
        builder.onResolve({ filter: /^@\// }, args => ({ path: Bun.resolveSync(resolve(import.meta.dir, "../src", args.path.slice(2)), import.meta.dir) }));
    } }] });
    if (!build.success) throw new Error(build.logs.join("\n"));
    const script = await build.outputs[0].text();
    const css = [await compileAppGlobalsStyles(), readAppWorkspaceProductSource()].join("\n");
    server = Bun.serve({ port: 0, async fetch(request) {
        const path = new URL(request.url).pathname;
        if (path === "/harness.js") return new Response(script, { headers: { "Content-Type": "text/javascript" } });
        if (path === "/app.css") return new Response(css, { headers: { "Content-Type": "text/css" } });
        if (path.startsWith("/api/")) {
            calls.push(path);
            if (path.includes("by-operation")) return Response.json({ code: 404, msg: "不存在" }, { status: 404 });
            if (path === "/api/tasks/quote") return Response.json({ code: 0, data: { id: "quote", units: 1, unitCredits: 12, totalCredits: 12, priceVersion: 1, expiresAt: "2099-01-01" } });
            if (path === "/api/tasks" && request.method === "GET") return Response.json({ code: 0, data: [] });
            if (path === "/api/tasks") return rejection
                ? Response.json({ code: 403, msg: "积分不足", reason: "insufficient_credits" }, { status: 403 })
                : Response.json({ code: 0, data: { id: "task", type: "canvas_image", status: "queued", prompt: "测试", attempts: 0, createdAt: "2026-01-01", updatedAt: "2026-01-01" } });
            if (path === "/api/credits") {
                await balanceGate;
                return balanceFailure ? Response.json({ code: 503, msg: "读取失败" }, { status: 503 }) : Response.json({ code: 0, data: { available: 3, frozen: 0 } });
            }
            return Response.json({ code: 0, data: {} });
        }
        return new Response('<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/app.css"></head><body><div id="root"></div><script type="module" src="/harness.js"></script></body></html>', { headers: { "Content-Type": "text/html" } });
    } });
    const executablePath = [process.env.CHROME_PATH, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "C:/Program Files/Google/Chrome Dev/Application/chrome.exe", "C:/Program Files/Google/Chrome/Application/chrome.exe"].find((path): path is string => Boolean(path && existsSync(path)));
    browser = await chromium.launch({ executablePath, headless: true });
}, 60_000);

afterAll(async () => { await browser?.close(); server?.stop(true); });

for (const theme of ["light", "dark"]) for (const mobile of [false, true]) test(`${theme} ${mobile ? "手机" : "桌面"}：不足提示、解除忙碌、关闭和积分导航`, async () => {
    rejection = true; balanceFailure = false; balanceGate = undefined; calls = [];
    const page = await browser.newPage({ viewport: mobile ? { width: 390, height: 844 } : { width: 1280, height: 800 } });
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    try {
        await page.goto(`${server.url}?theme=${theme}`);
        await page.getByRole("button", { name: "生成", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await dialog.getByText("还差", { exact: false }).waitFor();
        const text = await dialog.innerText();
        expect(text).toContain("12 积分"); expect(text).toContain("3 积分"); expect(text).toContain("9 积分");
        expect(text).toContain("请联系管理员补充积分");
        expect(await page.getByTestId("busy").innerText()).toBe("false");
        expect(await page.locator(".ant-message-error").count()).toBe(0);
        expect(calls.filter((path) => path === "/api/tasks")).toHaveLength(1);
        const box = await dialog.boundingBox();
        expect(box!.x).toBeGreaterThanOrEqual(0);
        expect(box!.x + box!.width).toBeLessThanOrEqual(mobile ? 390 : 1280);
        expect(box!.height).toBeLessThan(mobile ? 844 : 800);
        if (process.env.CREDIT_SCREENSHOT_DIR) await page.screenshot({ path: resolve(process.env.CREDIT_SCREENSHOT_DIR, `credit-${theme}-${mobile ? "mobile" : "desktop"}.png`) });
        await dialog.getByRole("button", { name: "返回创作" }).click();
        await dialog.waitFor({ state: "hidden" });
        expect(await page.getByTestId("route").innerText()).toBe("/create");
        await page.getByRole("button", { name: "生成", exact: true }).click();
        await dialog.getByRole("button", { name: "查看我的积分" }).click();
        await dialog.waitFor({ state: "hidden" });
        expect(await page.getByTestId("route").innerText()).toBe("/credits");
        expect(errors).toEqual([]);
    } finally { await page.close(); }
}, 20_000);

test("余额查询失败仍显示所需积分，不假设当前余额或差额", async () => {
    rejection = true; balanceFailure = true; balanceGate = undefined;
    const page = await browser.newPage();
    try {
        await page.goto(server.url.toString());
        await page.getByRole("button", { name: "生成", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await dialog.getByText("当前可用积分暂时无法读取", { exact: false }).waitFor();
        expect(await dialog.innerText()).toContain("12 积分");
        expect(await dialog.innerText()).not.toContain("0 积分");
        expect(await dialog.innerText()).not.toContain("还差");
    } finally { await page.close(); }
});

test("批次积分不足停在失败态，定时调度不自动重新提交", async () => {
    const page = await browser.newPage();
    try {
        await page.goto(`${server.url}?scenario=batch`);
        await page.getByRole("button", { name: "提交批次" }).click();
        await page.getByTestId("batch-status").getByText("failed", { exact: true }).waitFor();
        expect(await page.getByTestId("batch-error").innerText()).toBe("insufficient_credits");
        await page.waitForTimeout(4500);
        expect(await page.getByTestId("submits").innerText()).toBe("1");
    } finally { await page.close(); }
}, 15_000);

test("余额读取中即可返回创作，迟到响应不会重新打开；积分充足直接提交", async () => {
    rejection = true; balanceFailure = false;
    let release!: () => void;
    balanceGate = new Promise((resolve) => { release = resolve; });
    const page = await browser.newPage();
    try {
        await page.goto(server.url.toString());
        await page.getByRole("button", { name: "生成", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await dialog.getByText("正在读取可用积分…").waitFor();
        expect(await page.getByTestId("busy").innerText()).toBe("false");
        await dialog.getByRole("button", { name: "返回创作" }).click();
        release(); balanceGate = undefined;
        await dialog.waitFor({ state: "hidden" });
        rejection = false; calls = [];
        await page.getByRole("button", { name: "生成", exact: true }).click();
        await page.getByTestId("result").getByText("task", { exact: true }).waitFor();
        expect(await page.getByRole("dialog").count()).toBe(0);
        expect(calls.filter((path) => path === "/api/tasks")).toHaveLength(1);
        expect(calls).not.toContain("/api/credits");
    } finally { release(); balanceGate = undefined; await page.close(); }
});
