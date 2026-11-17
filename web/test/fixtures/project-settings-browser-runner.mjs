import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { chromium } from "playwright";

const [url, scenario] = process.argv.slice(2);
const executablePath = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, process.env.CHROME_PATH, "C:/Program Files/Google/Chrome/Application/chrome.exe", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"].find((path) => path && existsSync(path));
const browser = await chromium.launch({ executablePath, headless: true, timeout: 10_000 });
const page = await browser.newPage();
page.setDefaultTimeout(8_000);
page.on("pageerror", (error) => console.error(error));

async function patches() {
    return (await fetch(new URL("/harness/patches", url))).json();
}

async function waitForPatch() {
    const deadline = Date.now() + 4_000;
    while (Date.now() < deadline) {
        if ((await patches()).patchHits > 0) return;
        await new Promise((resolve) => setTimeout(resolve, 20));
    }
    throw new Error("项目保存未发出请求");
}

async function externalUpdate() {
    const response = await fetch(new URL("/api/projects/project-a", url), { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ name: "另一设备已保存", expectedRevision: 1 }) });
    assert.equal(response.status, 200);
}

async function inspectDrafts() {
    return page.evaluate(() => window.__projectSettingsHarness.inspectDrafts());
}

try {
    await page.goto(new URL(scenario === "library" ? "?platform&library" : scenario.startsWith("chapter-") ? "?platform&chapter" : scenario.startsWith("draft-") ? "?platform" : "", url).toString());
    await page.getByTestId("project-id").waitFor();

    if (scenario === "archive") {
        await page.getByRole("button", { name: "归档项目" }).click();
        await page.getByRole("button", { name: "确认归档" }).click();
        await page.getByRole("button", { name: "恢复项目" }).waitFor();
        assert.equal(await page.getByTestId("project-status").textContent(), "archived");
        await page.getByRole("button", { name: "恢复项目" }).click();
        await page.getByRole("button", { name: "确认恢复" }).click();
        await page.getByRole("button", { name: "归档项目" }).waitFor();
        assert.deepEqual((await patches()).patches.map((entry) => entry.body.status), ["archived", "active"]);
    }

    if (scenario === "strict") {
        assert.ok(await page.evaluate(() => window.__projectSettingsHarness.strictSetups >= 2));
        await page.getByLabel("项目名称").fill("项目甲已改");
        await page.getByRole("button", { name: "保存设置" }).click();
        await page.getByRole("button", { name: "已保存" }).waitFor();
        assert.equal((await patches()).patches[0].body.name, "项目甲已改");
    }

    if (scenario.startsWith("stale-")) {
        await page.getByLabel("项目名称").fill("项目甲已改");
        await page.getByRole("button", { name: "保存设置" }).click();
        await waitForPatch();
        await page.evaluate(() => window.__projectSettingsHarness.switchProjectABA());
        await fetch(new URL("/harness/release-patch", url), { method: "POST" });
        await page.waitForTimeout(500);
        assert.equal(await page.getByLabel("项目名称").inputValue(), "项目甲");
        assert.equal(await page.evaluate(() => window.__projectSettingsHarness.refreshCount), 0);
        const toasts = await page.evaluate(() => window.__projectSettingsHarness.toasts.join("\n"));
        assert.ok(!toasts.includes("项目设置已保存") && !toasts.includes("网络中断"));
    }

    if (scenario === "conflict" || scenario === "draft-conflict") {
        await page.getByLabel("项目名称").fill("本机未保存草稿");
        await externalUpdate();
        await page.evaluate(() => window.__projectSettingsHarness.refreshCurrent());
        await page.waitForFunction(() => document.querySelector('[data-testid="project-revision"]')?.textContent === "2");
        if (scenario === "draft-conflict") {
            const reason = await page.evaluate(async () => {
                try { await window.__projectSettingsHarness.flushDrafts(); return ""; }
                catch (error) { return error.reason; }
            });
            assert.equal(reason, "revision_conflict");
            assert.equal((await inspectDrafts())[0].hasUnsynced, true);
        } else {
            await page.getByRole("button", { name: "保存设置" }).click();
            await page.waitForFunction(() => window.__projectSettingsHarness.toasts.some((text) => text.includes("草稿已保留")));
        }
        assert.equal((await patches()).patches.at(-1).body.expectedRevision, 1);
        assert.equal(await page.getByLabel("项目名称").inputValue(), "本机未保存草稿");
        const serverProject = await (await fetch(new URL("/api/projects/project-a", url))).json();
        assert.equal(serverProject.data.project.name, "另一设备已保存");
    }

    if (scenario === "draft-flush") {
        await page.getByLabel("项目名称").fill("退出前同步的项目");
        assert.equal((await inspectDrafts())[0].hasUnsynced, true);
        const result = await page.evaluate(async () => {
            await window.__projectSettingsHarness.flushDrafts();
            return window.__projectSettingsHarness.inspectDrafts();
        });
        assert.equal(result[0].hasUnsynced, false);
        assert.equal((await patches()).patchHits, 1);
    }

    if (scenario === "draft-pending") {
        await page.getByLabel("项目名称").fill("正在保存的项目");
        await page.getByRole("button", { name: "保存设置" }).click();
        await waitForPatch();
        await page.getByLabel("项目名称").fill("保存期间继续编辑");
        await page.evaluate(() => { window.__pendingDraftFlush = window.__projectSettingsHarness.flushDrafts(); });
        await fetch(new URL("/harness/release-patch", url), { method: "POST" });
        await page.evaluate(() => window.__pendingDraftFlush);
        assert.equal((await patches()).patchHits, 1);
        assert.equal((await inspectDrafts())[0].hasUnsynced, true);
        await page.evaluate(() => window.__projectSettingsHarness.flushDrafts());
        assert.equal((await patches()).patchHits, 2);
        assert.equal((await patches()).patches.at(-1).body.expectedRevision, 2);
        assert.equal((await inspectDrafts())[0].hasUnsynced, false);
    }

    if (scenario.startsWith("chapter-")) {
        await page.getByLabel("章节正文").fill("<p>本机未保存正文</p>");
        assert.equal((await inspectDrafts())[0].hasUnsynced, true);
        if (scenario === "chapter-conflict") {
            await externalUpdate();
            const reason = await page.evaluate(async () => {
                try { await window.__projectSettingsHarness.flushDrafts(); return ""; }
                catch (error) { return error.reason; }
            });
            assert.equal(reason, "revision_conflict");
            assert.equal((await inspectDrafts())[0].hasUnsynced, true);
            assert.equal(await page.getByTestId("chapter-revision").textContent(), "1");
            assert.equal(await page.getByLabel("章节正文").inputValue(), "<p>本机未保存正文</p>");
        } else if (scenario === "chapter-pending") {
            await page.getByRole("button", { name: "保存章节" }).click();
            await waitForPatch();
            await page.getByLabel("章节正文").fill("<p>保存期间新增正文</p>");
            await page.evaluate(() => { window.__pendingDraftFlush = window.__projectSettingsHarness.flushDrafts(); });
            await fetch(new URL("/harness/release-patch", url), { method: "POST" });
            await page.evaluate(() => window.__pendingDraftFlush);
            assert.equal((await patches()).patchHits, 1);
            assert.equal((await inspectDrafts())[0].hasUnsynced, true);
            await page.evaluate(() => window.__projectSettingsHarness.flushDrafts());
            assert.equal((await patches()).patchHits, 2);
            assert.equal((await patches()).patches.at(-1).body.expectedRevision, 2);
            assert.equal((await inspectDrafts())[0].hasUnsynced, false);
        } else {
            const result = await page.evaluate(async () => {
                await window.__projectSettingsHarness.flushDrafts();
                return window.__projectSettingsHarness.inspectDrafts();
            });
            assert.equal(result[0].hasUnsynced, false);
            assert.equal((await patches()).patchHits, 1);
        }
    }

    if (scenario === "library") {
        const sidebar = page.locator("aside");
        assert.equal(await sidebar.getByRole("link", { name: "项目", exact: true }).getAttribute("href"), "/projects");
        assert.equal(await sidebar.getByRole("link", { name: "画布", exact: true }).getAttribute("href"), "/canvas");
        assert.equal(await sidebar.getByRole("link", { name: "素材", exact: true }).getAttribute("href"), "/assets");
        assert.equal(await sidebar.getByRole("link", { name: "技能", exact: true }).count(), 0);
        assert.equal(await page.getByRole("button", { name: "AI 生成章节", exact: true }).count(), 0);
        await sidebar.getByRole("link", { name: "画布", exact: true }).click();
        await page.getByText("画布列表", { exact: true }).waitFor();
        await sidebar.getByRole("link", { name: "素材", exact: true }).click();
        await page.getByText("素材列表", { exact: true }).waitFor();
        await sidebar.getByRole("link", { name: "新建项目", exact: true }).click();
        await page.getByRole("dialog", { name: "创建短剧项目" }).waitFor();
        await page.getByLabel("项目名称", { exact: true }).fill("平台新项目");
        await page.getByRole("button", { name: "创建项目", exact: true }).click();
        await page.getByText("项目创建完成", { exact: true }).waitFor();
        assert.equal(await page.getByTestId("route-path").textContent(), "/projects/new-project/overview");
        assert.equal((await patches()).patches.at(-1).body.name, "平台新项目");
    }
} finally {
    await browser.close();
}
