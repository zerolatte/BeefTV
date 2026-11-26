import assert from "node:assert/strict";
import { createServer } from "node:http";
import { existsSync } from "node:fs";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright";

// 使用构建产物与模拟 API 检查真实页面，不连接业务服务或修改真实账号。
// 先运行 bun run build，再用 node scripts/institute-workspace-smoke.mjs；可用 CHROME_PATH 指定已安装的浏览器。
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const dist = path.join(webRoot, "dist");
const output = path.resolve(webRoot, "../.local/cache/institute-workspace");
await mkdir(output, { recursive: true });
const mime = { ".html": "text/html; charset=utf-8", ".js": "application/javascript", ".css": "text/css", ".png": "image/png", ".jpg": "image/jpeg", ".svg": "image/svg+xml", ".woff2": "font/woff2" };
const server = createServer(async (request, response) => {
    try {
        const route = new URL(request.url, "http://localhost").pathname;
        const requested = path.resolve(dist, `.${decodeURIComponent(route)}`);
        if (requested !== dist && !requested.startsWith(dist + path.sep)) { response.writeHead(400); response.end(); return; }
        const file = path.extname(requested) && existsSync(requested) ? requested : path.join(dist, "index.html");
        response.writeHead(200, { "Content-Type": mime[path.extname(file)] || "application/octet-stream" });
        response.end(await readFile(file));
    } catch { response.writeHead(500); response.end(); }
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${server.address().port}`;
const browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || undefined, headless: true });
const report = { layouts: [], interactions: [], states: [], requests: [], errors: [], contrast: [], notes: [] };
let activePage;
const stamp = "2026-01-01T08:00:00Z";
const users = Array.from({ length: 31 }, (_, i) => ({ id: `sample-user-${i}`, username: `creator${i}`, displayName: `创作人员 ${i + 1}`, role: i === 0 ? "admin" : "user", status: "active", mustChangePassword: false }));
const projects = ["研究院形象短片", "城市记忆 · 分镜", "光影实验"].map((title, i) => ({ id: `sample-project-${i}`, title, revision: 1, createdAt: stamp, updatedAt: stamp, nodes: [], connections: [], chatSessions: [], activeChatId: null, backgroundMode: "dots", showImageInfo: true, viewport: { x: 0, y: 0, zoom: 1 }, directorScenes: [] }));
const assets = ["开场旁白", "第二幕分镜", "人物设定"].map((title, i) => ({ id: `sample-asset-${i}`, kind: "text", title, coverUrl: "", tags: [], createdAt: stamp, updatedAt: stamp, status: "confirmed", source: "upload", data: { content: "晨光穿过窗户，故事从这里开始。" } }));
const ledger = Array.from({ length: 31 }, (_, i) => ({ id: `sample-ledger-${i}`, kind: "adjust", amount: 100, reason: "创作积分发放", createdAt: stamp }));

async function newPage(theme, width = 1440, role = "admin", mode = "normal") {
    const context = await browser.newContext({ viewport: { width, height: 900 }, colorScheme: theme, reducedMotion: "reduce" });
    await context.addInitScript(selected => localStorage.setItem("infinite-canvas:theme_store", JSON.stringify({ state: { theme: selected, preference: selected }, version: 0 })), theme);
    const state = { mode, role, delay: 0, errorRoutes: new Set(), deferred: null };
    await context.route("**/api/**", async route => {
        const url = new URL(route.request().url());
        const pathname = url.pathname.replace(/^\/api/, "");
        report.requests.push({ path: pathname, query: url.search, method: route.request().method(), role });
        const json = (data, status = 200, msg = "") => route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ code: status === 200 ? 0 : status, data, msg }) });
        if (state.delay && state.deferred?.test(pathname)) await new Promise(resolve => setTimeout(resolve, state.delay));
        if (state.errorRoutes.has(pathname)) return json(null, 503, "测试：暂时无法读取，请稍后重试。");
        const user = { ...users[0], role };
        const list = value => state.mode === "empty" ? [] : value;
        const paginate = items => ({ items: items.slice((Number(url.searchParams.get("page") || 1) - 1) * 30, Number(url.searchParams.get("page") || 1) * 30), total: items.length });
        if (pathname === "/auth/session") return json({ user });
        if (pathname === "/workspace/bootstrap") return json({ contractVersion: 1, profile: "platform", user, workspace: { id: user.id, name: "测试工作区", owner: "platform", storage: "sqlite" }, storageMode: "local", capabilities: { localAssets: true, providerCalls: true }, runtimeLimits: { activeTaskLimit: 3, resourceUploadMB: 50 } });
        if (pathname === "/workspace/model-config") return json({ config: { channels: [] }, revision: 1, health: "ready", source: "builtin+local" });
        if (pathname === "/canvas-projects") return json({ projects: list(projects), total: list(projects).length, page: 1, pageSize: 50, hasMore: false });
        if (/^\/canvas-projects\/sample-project-\d+$/.test(pathname)) return json({ project: projects.find(item => pathname.endsWith(item.id)) });
        if (pathname === "/canvas-folders" || pathname === "/asset-folders") return json({ folders: [] });
        if (pathname === "/assets") {
            const q = url.searchParams.get("q") || "";
            const kind = url.searchParams.get("kind");
            const filtered = list(assets).filter(item => item.title.includes(q) && (!kind || kind === "all" || kind === item.kind));
            return json({ assets: filtered, total: filtered.length, page: 1, pageSize: 40, hasMore: false, kindCounts: { text: list(assets).length }, categoryCounts: {}, folderCounts: {}, favoriteTotal: 0, recentTotal: filtered.length, projectCounts: {}, generatedTotal: 0, generatedKindCounts: {} });
        }
        if (pathname === "/assets/batch") return json({ assets: [] });
        if (pathname === "/credits") return json({ available: 300, frozen: 20 });
        if (pathname === "/credits/ledger") return json(paginate(list(ledger)));
        if (pathname === "/admin/accounts") return json(paginate(list(users).filter(item => item.username.includes(url.searchParams.get("q") || ""))));
        if (pathname === "/admin/channels") return json({ channels: list([{ id: "sample-channel", name: "创作模型", enabled: true, hasApiKey: true, models: ["sample-model"] }]), total: state.mode === "empty" ? 0 : 1 });
        if (pathname === "/admin/channels/sample-channel/models") return json(list([{ id: "sample-model", modelKey: "sample", displayName: "分镜助手", capability: "text", protocol: "chat-completion", enabled: true, variants: [] }]));
        if (pathname === "/admin/prices") return json(list([{ modelId: "sample-model", variantId: "", unitCredits: 1, published: true }]));
        if (pathname === "/admin/usage") return json({ items: [], total: 0 });
        if (pathname === "/model-catalog" || pathname === "/models") return json({ items: [], channels: [], models: [] });
        if (pathname === "/projects") return json({ projects: [] });
        if (pathname === "/tasks") return json({ tasks: [], total: 0 });
        return json({ items: [], projects: [], folders: [], assets: [], snapshots: [], total: 0 });
    });
    const page = await context.newPage();
    activePage = page;
    page.on("pageerror", error => report.errors.push(error.message));
    page.setDefaultTimeout(30000);
    // 本次模拟会话的个人资产库读 IndexedDB；只给隔离测试上下文写入合成素材。
    await context.route(origin + "/__brand-test-seed", route => route.fulfill({ contentType: "text/html", body: "<html></html>" }));
    await page.goto(origin + "/__brand-test-seed");
    await page.evaluate(async sampleAssets => {
        const db = await new Promise((resolve, reject) => {
            const request = indexedDB.open("infinite-canvas", 1);
            request.onupgradeneeded = () => {
                for (const store of ["app_state", "canvas_folder_pending", "image_files", "media_files", "resource_blobs", "resource_blob_meta", "drawing_documents", "drawing_previews", "drawing_generation_renders"]) request.result.createObjectStore(store);
            };
            request.onsuccess = () => resolve(request.result);
            request.onerror = () => reject(request.error);
        });
        await new Promise((resolve, reject) => {
            const transaction = db.transaction("app_state", "readwrite");
            transaction.objectStore("app_state").put(JSON.stringify({ state: { assets: sampleAssets }, version: 0, storageRevision: 1, tombstones: { assets: {} } }), "infinite-canvas:asset_store:user:sample-user-0");
            transaction.oncomplete = resolve;
            transaction.onerror = () => reject(transaction.error);
        });
        db.close();
    }, mode === "empty" ? [] : assets);
    return { page, context, state };
}

async function visit(page, route) {
    await page.goto(origin + route);
    await page.locator(".app-institute-workspace").waitFor();
    await page.locator(".app-workspace-stage main").waitFor();
    if (route === "/") await page.getByText("最近项目", { exact: true }).waitFor();
    if (route === "/project") await page.getByRole("heading", { name: "全部项目", exact: true }).waitFor();
    if (route.startsWith("/assets")) await page.getByRole("heading", { name: "个人资产库", exact: true }).waitFor();
    if (route === "/credits") await page.getByRole("heading", { name: "我的积分", exact: true }).waitFor();
    if (route === "/admin") await page.getByRole("heading", { name: "平台管理", exact: true }).waitFor();
}

async function inspect(page, label) {
    const metrics = await page.evaluate(() => {
        const root = document.querySelector(".app-institute-workspace");
        const stage = document.querySelector(".app-workspace-stage");
        const style = getComputedStyle(root);
        return { width: innerWidth, rootWidth: root.scrollWidth, stageWidth: stage.clientWidth, stageScroll: stage.scrollWidth, sidebar: document.querySelector(".app-workspace-sidebar").getBoundingClientRect().width, action: style.getPropertyValue("--user-action-bg").trim(), background: getComputedStyle(stage).backgroundColor, bodyBrand: document.body.classList.contains("app-institute-overlays") };
    });
    assert.ok(metrics.rootWidth <= metrics.width + 1, `${label} 根节点横向溢出`);
    assert.ok(metrics.stageScroll <= metrics.stageWidth + 1, `${label} 主内容横向溢出`);
    assert.equal(metrics.action, "#0077b5");
    assert.ok(metrics.bodyBrand);
    if (metrics.width < 1024) assert.equal(metrics.stageWidth, metrics.width, "关闭移动侧栏后不得保留占位");
    report.layouts.push({ label, ...metrics });
}

async function contrast(locator, label) {
    await settle(locator.page());
    const colors = await locator.evaluate(element => {
        const style = getComputedStyle(element);
        let background = style.backgroundColor;
        for (let parent = element.parentElement; parent && background === "rgba(0, 0, 0, 0)"; parent = parent.parentElement) background = getComputedStyle(parent).backgroundColor;
        return { color: style.color, background };
    });
    const luminance = color => color.match(/[\d.]+/g).slice(0, 3).map(Number).map(value => value / 255).map(value => value <= .04045 ? value / 12.92 : ((value + .055) / 1.055) ** 2.4).reduce((sum, value, i) => sum + value * [.2126, .7152, .0722][i], 0);
    const pair = [luminance(colors.color), luminance(colors.background)].sort((a, b) => b - a);
    const ratio = (pair[0] + .05) / (pair[1] + .05);
    assert.ok(ratio >= 4.5, `${label} 对比度不足：${ratio}，${JSON.stringify(colors)}`);
    report.contrast.push({ label, ratio: Number(ratio.toFixed(2)), ...colors });
}

async function settle(page) {
    // 等待进入动画的类名与布局都稳定，避免只截到弹窗遮罩的中间帧。
    await page.evaluate(async () => {
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        await Promise.all(document.getAnimations().filter(animation => animation.effect?.getTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {})));
    });
}

async function capture(page, name) {
    await settle(page);
    await page.screenshot({ path: path.join(output, name + ".png"), animations: "disabled" });
}

async function checkStates(theme) {
    const empty = await newPage(theme, 390, "admin", "empty");
    const expectations = [
        ["/", "创建第一个画布", "home"],
        ["/project", "开始创作", "projects"],
        ["/assets?tab=personal", "上传资产", "assets"],
        ["/credits", "暂无积分流水", "credits"],
        ["/admin", "暂无账号", "admin"],
    ];
    for (const [route, text, name] of expectations) {
        await visit(empty.page, route);
        await empty.page.getByText(text, { exact: true }).first().waitFor();
        await inspect(empty.page, `${theme} ${name} 空数据`);
        await capture(empty.page, `${name}-empty-${theme}`);
        report.states.push(`${theme} ${name} 空数据`);
    }
    await empty.context.close();

    for (const [route, api, name, title] of [["/credits", "/credits/ledger", "credits", "流水读取失败"], ["/admin", "/admin/accounts", "admin", "账号读取失败"]]) {
        const scenario = await newPage(theme, 390);
        scenario.state.delay = 1500;
        scenario.state.deferred = new RegExp(`^${api}$`);
        await visit(scenario.page, route);
        await scenario.page.locator(".ant-spin-spinning").waitFor();
        await inspect(scenario.page, `${theme} ${name} 加载`);
        await capture(scenario.page, `${name}-loading-${theme}`);
        await scenario.page.locator(".ant-spin-spinning").waitFor({ state: "hidden" });
        scenario.state.delay = 0;
        scenario.state.errorRoutes.add(api);
        await visit(scenario.page, route);
        await scenario.page.getByText(title, { exact: true }).waitFor();
        assert.ok(await scenario.page.getByText("测试：暂时无法读取，请稍后重试。", { exact: true }).isVisible());
        await inspect(scenario.page, `${theme} ${name} 错误`);
        await capture(scenario.page, `${name}-error-${theme}`);
        scenario.state.errorRoutes.clear();
        await scenario.page.getByRole("button", { name: /^重\s*试$/ }).click();
        await scenario.page.getByRole("cell", { name: name === "admin" ? "creator0" : "创作积分发放", exact: true }).first().waitFor();
        report.states.push(`${theme} ${name} 加载、错误、重试恢复`);
        await scenario.context.close();
    }
    const failed = await newPage(theme, 390);
    failed.state.errorRoutes.add("/canvas-projects");
    await visit(failed.page, "/project");
    await failed.page.getByRole("button", { name: "重试加载", exact: true }).first().waitFor();
    await inspect(failed.page, `${theme} projects 同步失败`);
    await capture(failed.page, `projects-error-${theme}`);
    failed.state.errorRoutes.clear();
    await failed.page.getByRole("button", { name: "重试加载", exact: true }).first().click();
    await failed.page.getByText("研究院形象短片", { exact: true }).waitFor();
    report.states.push(`${theme} projects 同步错误与重试恢复`);
    await failed.context.close();
}

try {
    const routes = [["/", "home"], ["/project", "projects"], ["/assets?tab=personal", "assets"], ["/credits", "credits"], ["/admin", "admin"]];
    for (const theme of ["light", "dark"]) {
        const { page, context } = await newPage(theme);
        for (const [route, name] of process.env.BRAND_SMOKE_FOCUS ? [] : routes) {
            await page.setViewportSize({ width: 1440, height: 900 });
            await visit(page, route);
            if (name === "assets") await page.getByText("开场旁白", { exact: true }).waitFor();
            if (name === "admin") await page.getByRole("cell", { name: "creator0", exact: true }).waitFor();
            if (name === "credits") await page.getByRole("cell", { name: "创作积分发放", exact: true }).first().waitFor();
            for (const width of [1440, 1024, 768, 390, 320]) {
                await page.setViewportSize({ width, height: 900 });
                await inspect(page, `${theme} ${name} ${width}`);
                if (width === 1440 || width === 390) await capture(page, `${name}-${theme}-${width}`);
                if (width < 1024) {
                    await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
                    await page.locator(".app-workspace-sidebar.is-mobile-expanded").waitFor();
                    const logo = page.locator(".app-workspace-institute-brand img");
                    assert.ok(await logo.isVisible());
                    const geometry = await logo.evaluate(image => ({ ratio: image.getBoundingClientRect().width / image.getBoundingClientRect().height, natural: image.naturalWidth / image.naturalHeight, filter: getComputedStyle(image).filter }));
                    assert.ok(Math.abs(geometry.ratio - geometry.natural) < .01);
                    assert.equal(geometry.filter, "none");
                    await page.locator(".app-workspace-sidebar-scrim").click({ position: { x: width - 20, y: 200 } });
                    await inspect(page, `${theme} ${name} ${width} 关闭抽屉`);
                }
            }
            console.log(`通过：${theme} ${name} 五种宽度、移动侧栏与页面布局`);
        }
        await page.setViewportSize({ width: 1440, height: 900 });
        await visit(page, "/");
        await contrast(page.locator('[data-nav-id="new"]'), `${theme} 主按钮`);
        await contrast(page.locator(".beeftv-home-identity"), `${theme} 首页标识`);
        await contrast(page.locator(".app-workspace-institute-brand > span"), `${theme} 字标说明`);
        assert.equal(await page.locator('[data-nav-id="new"]').evaluate(element => getComputedStyle(element).backgroundColor), "rgb(0, 119, 181)");
        await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
        await page.locator(".app-workspace-institute-icon img").waitFor();
        await page.waitForFunction(() => document.querySelector(".app-workspace-sidebar").getBoundingClientRect().width === 64, undefined, { timeout: 1500 });
        assert.equal(await page.locator(".app-workspace-sidebar").evaluate(element => element.getBoundingClientRect().width), 64);
        const icon = await page.locator(".app-workspace-institute-icon img").evaluate(async image => {
            await image.decode();
            return { ratio: image.clientWidth / image.clientHeight, natural: image.naturalWidth / image.naturalHeight, filter: getComputedStyle(image).filter };
        });
        assert.ok(Math.abs(icon.ratio - icon.natural) < .02);
        assert.equal(icon.filter, "none");
        await capture(page, `sidebar-collapsed-${theme}`);
        await page.getByRole("button", { name: "展开侧栏菜单", exact: true }).click();
        await page.waitForFunction(() => document.querySelector(".app-workspace-sidebar").getBoundingClientRect().width === 240);
        assert.equal(await page.locator(".app-workspace-sidebar").evaluate(element => element.getBoundingClientRect().width), 240);

        await visit(page, "/admin");
        const openAccount = page.getByRole("button", { name: "开通账号", exact: true });
        await contrast(openAccount, `${theme} 管理页主按钮`);
        assert.equal(await openAccount.evaluate(element => getComputedStyle(element).backgroundColor), "rgb(0, 119, 181)");
        await openAccount.click();
        const dialog = page.getByRole("dialog");
        await dialog.waitFor();
        const accountInput = dialog.getByLabel("用户名", { exact: true });
        await accountInput.focus();
        const focus = await accountInput.evaluate(element => ({ border: getComputedStyle(element).borderColor, focus: getComputedStyle(document.body).getPropertyValue("--user-focus").trim() }));
        assert.equal(focus.border, theme === "light" ? "rgb(0, 119, 181)" : "rgb(112, 200, 245)");
        await contrast(dialog.locator(".ant-btn-primary"), `${theme} 弹窗主按钮`);
        await capture(page, `account-dialog-${theme}`);
        for (const width of [390, 320]) {
            await page.setViewportSize({ width, height: 900 });
            await settle(page);
            const bounds = await dialog.evaluate(element => {
                const rect = element.getBoundingClientRect();
                return { left: rect.left, right: rect.right, width: innerWidth, scroll: element.scrollWidth, content: element.clientWidth };
            });
            assert.ok(bounds.left >= 0 && bounds.right <= bounds.width && bounds.scroll <= bounds.content + 1, "窄屏弹窗应完整呈现");
            await capture(page, `account-dialog-${theme}-${width}`);
        }
        await page.setViewportSize({ width: 1440, height: 900 });
        await page.keyboard.press("Escape");
        await dialog.waitFor({ state: "hidden" });
        const search = page.getByLabel("搜索账号", { exact: true });
        await search.fill("creator30");
        await search.press("Enter");
        await page.getByRole("cell", { name: "creator30", exact: true }).waitFor();
        await search.fill("");
        await search.press("Enter");
        await page.getByRole("button", { name: "下一页", exact: true }).click();
        await page.getByRole("cell", { name: "creator30", exact: true }).waitFor();
        await contrast(page.locator('.app-pagination-btn[aria-current="page"]'), `${theme} 分页选中`);
        assert.equal(await page.locator('.app-pagination-btn[aria-current="page"]').evaluate(element => getComputedStyle(element).color), theme === "light" ? "rgb(0, 103, 158)" : "rgb(154, 216, 250)");
        await page.getByRole("tab", { name: "模型与价格", exact: true }).click();
        await contrast(page.getByRole("tab", { name: "模型与价格", exact: true }), `${theme} 管理标签选中`);
        await page.getByRole("button", { name: "新增渠道", exact: true }).click();
        await dialog.waitFor();
        const switchColor = await dialog.getByRole("switch").evaluate(element => getComputedStyle(element).backgroundColor);
        assert.notEqual(switchColor, "rgb(0, 119, 181)", "开关不能误用主操作色");
        await capture(page, `channel-dialog-${theme}`);
        await page.keyboard.press("Escape");
        await page.getByRole("tab", { name: "用量与核对", exact: true }).click();
        await page.getByLabel("用量状态", { exact: true }).click();
        await page.locator(".ant-select-dropdown:visible").getByText("待核对", { exact: true }).click();
        await page.getByText("暂无用量记录", { exact: true }).waitFor();
        await capture(page, `usage-${theme}`);
        report.interactions.push(`${theme}：侧栏折叠、账号搜索与分页、账号弹窗焦点和主按钮、开关语义、用量筛选`);

        await visit(page, "/project");
        await page.getByRole("button", { name: "搜索项目", exact: true }).click();
        await page.getByRole("textbox", { name: "搜索项目", exact: true }).fill("形象");
        await page.getByText("研究院形象短片", { exact: true }).waitFor();
        assert.equal(await page.locator(".workspace-expandable-search").evaluate(element => getComputedStyle(element).outlineColor), theme === "light" ? "rgb(0, 119, 181)" : "rgb(112, 200, 245)");
        assert.equal(await page.getByText("光影实验", { exact: true }).count(), 0);
        await visit(page, "/assets?tab=personal");
        await contrast(page.locator(".assets-library-source-rail-item.is-active"), `${theme} 资产来源选中`);
        await page.locator(".assets-kind-tab").filter({ hasText: "图片" }).click();
        await contrast(page.locator(".assets-kind-tab.is-active"), `${theme} 资产分类选中`);
        await contrast(page.locator(".assets-kind-tab.is-active span"), `${theme} 资产分类计数`);
        await page.getByRole("button", { name: "新建", exact: true }).click();
        await page.getByRole("menuitem", { name: "新建文件夹", exact: true }).click();
        await capture(page, `asset-folder-${theme}`);
        await page.keyboard.press("Escape");
        report.interactions.push(`${theme}：项目搜索、资产分类与新建文件夹入口`);

        await visit(page, "/project");
        await page.getByText("研究院形象短片", { exact: true }).click();
        await page.waitForURL("**/canvas/sample-project-0");
        await page.waitForFunction(() => !document.querySelector(".app-institute-workspace") && !document.body.classList.contains("app-institute-overlays"));
        assert.notEqual(await page.locator("body").evaluate(element => getComputedStyle(element).getPropertyValue("--user-action-bg").trim()), "#0077b5");
        await page.getByRole("button", { name: "打开画布菜单", exact: true }).waitFor();
        assert.equal(await page.locator(".app-workspace-sidebar").count(), 0);
        await capture(page, `canvas-isolation-${theme}`);
        await page.getByRole("button", { name: "打开画布菜单", exact: true }).click();
        await page.getByRole("menuitem", { name: "回到主页", exact: true }).click();
        await page.locator(".app-institute-workspace").waitFor();
        await inspect(page, `${theme} 画布返回工作区`);
        report.interactions.push(`${theme}：工作区→画布→工作区标记清理与恢复`);
        await page.keyboard.press("Tab");
        await page.locator('[data-nav-id="canvas"]').focus();
        const keyboardFocus = await page.locator('[data-nav-id="canvas"]').evaluate(element => ({ style: getComputedStyle(element).outlineStyle, width: getComputedStyle(element).outlineWidth }));
        assert.equal(keyboardFocus.style, "solid");
        assert.equal(keyboardFocus.width, "2px");
        await page.setViewportSize({ width: 390, height: 900 });
        await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
        await capture(page, `sidebar-mobile-${theme}`);
        await page.locator('[data-nav-id="canvas"]').click();
        await page.waitForURL("**/project");
        assert.equal(await page.locator(".app-workspace-sidebar-scrim").count(), 0);
        await inspect(page, `${theme} 移动导航关闭`);
        report.interactions.push(`${theme}：键盘焦点、窄屏弹窗、移动导航关闭且无占位`);
        await context.close();
        await checkStates(theme);
    }

    const user = await newPage("light", 1440, "user");
    await visit(user.page, "/");
    assert.equal(await user.page.locator('[data-nav-id="admin"]').count(), 0);
    await user.page.goto(origin + "/admin");
    await user.page.waitForURL(origin + "/");
    await user.page.getByText("最近项目", { exact: true }).waitFor();
    await user.context.close();
    report.interactions.push("普通用户不显示管理入口，直接访问管理路由仍被拦截");
    report.notes.push("本次构建与模拟会话的首页、个人资产库走本地缓存；两页远端请求的加载／错误分支未触发。本次覆盖正常与空数据，并保留原有业务逻辑。");
    assert.deepEqual(report.errors, []);
    console.log(`通过：${report.layouts.length} 个布局场景，${report.interactions.length} 组交互，${report.states.length} 组状态，${report.contrast.length} 项对比度。`);
} catch (error) {
    if (activePage && !activePage.isClosed()) {
        console.error(await activePage.locator("body").innerText());
        await activePage.screenshot({ path: path.join(output, "failure.png") });
    }
    throw error;
} finally {
    await writeFile(path.join(output, "browser-report.json"), JSON.stringify(report, null, 2));
    await browser.close();
    await new Promise(resolve => server.close(resolve));
}
