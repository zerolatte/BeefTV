import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { chromium } from "playwright";

const [url, scenario] = process.argv.slice(2);
const executablePath = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, process.env.CHROME_PATH, "C:/Program Files/Google/Chrome/Application/chrome.exe", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"].find((path) => path && existsSync(path));
const browser = await chromium.launch({ executablePath, headless: true, timeout: 10_000 });
const context = await browser.newContext();
const page = await context.newPage();
page.setDefaultTimeout(8_000);
page.on("pageerror", (error) => console.error(error));

async function login(username = "alice") {
    await page.getByLabel("用户名", { exact: true }).fill(username);
    await page.getByLabel("密码", { exact: true }).fill("password-long-enough");
    await page.getByRole("button", { name: "登录", exact: true }).click();
}

try {
    await page.goto(url);
    await page.getByRole("heading", { name: "登录工作区" }).waitFor();

    if (scenario === "password") {
        assert.equal(await page.getByRole("heading", { name: "创作工作区" }).count(), 0);
        await login("new-user");
        await page.getByRole("heading", { name: "修改密码" }).waitFor();
        assert.equal(await page.getByRole("heading", { name: "创作工作区" }).count(), 0);
        await page.getByLabel("当前密码", { exact: true }).fill("password-long-enough");
        await page.getByLabel("新密码", { exact: true }).fill("replacement-password");
        await page.getByLabel("确认新密码", { exact: true }).fill("replacement-password");
        await page.getByRole("button", { name: "保存新密码" }).click();
        await page.getByRole("heading", { name: "创作工作区" }).waitFor();
        assert.equal(await page.evaluate(() => window.platformTest.hydrations), 1);
    }

    if (scenario === "recovery") {
        await login();
        await page.getByLabel("本地草稿").fill("尚未保存的分镜");
        await page.getByRole("button", { name: "编辑浮层草稿" }).click();
        await page.getByLabel("浮层草稿").fill("浮层输入保持");
        const before = await page.evaluate(() => ({ mounts: window.platformTest.mounts, unmounts: window.platformTest.unmounts }));
        await page.evaluate(() => window.platformTest.invalidate());
        await page.getByRole("heading", { name: "恢复工作区会话" }).waitFor();
        assert.equal(await page.locator("[inert] input").first().inputValue(), "尚未保存的分镜");
        assert.equal(await page.getByLabel("浮层草稿").isVisible(), false);
        assert.equal(await page.getByLabel("原账号").inputValue(), "alice");
        assert.notEqual(await page.getByLabel("原账号").getAttribute("readonly"), null);
        await page.getByLabel("密码", { exact: true }).fill("password-long-enough");
        await page.getByRole("button", { name: "重新认证" }).click();
        await page.getByRole("heading", { name: "创作工作区" }).waitFor();
        assert.equal(await page.getByLabel("本地草稿").inputValue(), "尚未保存的分镜");
        assert.equal(await page.getByLabel("浮层草稿").inputValue(), "浮层输入保持");
        assert.equal(await page.getByLabel("浮层草稿").isVisible(), true);
        assert.deepEqual(await page.evaluate(() => ({ mounts: window.platformTest.mounts, unmounts: window.platformTest.unmounts })), before);
        assert.equal(await page.evaluate(() => window.platformTest.hydrations), 1);
        assert.ok(await page.evaluate(() => window.platformTest.flushes) > 0);
    }

    if (scenario === "logout") {
        await login();
        await page.getByLabel("本地草稿").fill("失败草稿");
        await page.evaluate(() => { window.platformTest.failFlush = true; });
        await page.getByRole("button", { name: "退出账号", exact: true }).click();
        await page.getByRole("button", { name: "同步并退出" }).click();
        await page.getByText("草稿尚未同步，请继续同步或明确放弃。").waitFor();
        assert.equal(await page.locator("[inert] input").inputValue(), "失败草稿");
        await page.evaluate(() => { window.platformTest.failClear = true; });
        await page.getByRole("button", { name: "确认放弃并退出" }).click();
        await page.getByText("缓存清理失败，暂时不能切换账号。").waitFor();
        assert.equal(await page.getByRole("heading", { name: "登录工作区" }).count(), 0);
        assert.equal(await page.getByRole("button", { name: "返回工作区" }).count(), 0);
        await page.evaluate(() => { window.platformTest.failClear = false; });
        await page.getByRole("button", { name: "确认放弃并退出" }).click();
        await page.getByRole("heading", { name: "登录工作区" }).waitFor();
        assert.equal(await page.evaluate(() => window.platformTest.clears), 1);
    }

    if (scenario === "cross-tab") {
        await login();
        await page.getByLabel("本地草稿").fill("标签页甲草稿");
        const other = await context.newPage();
        await other.goto(url);
        await other.getByRole("heading", { name: "创作工作区" }).waitFor();
        await other.getByRole("button", { name: "退出账号", exact: true }).click();
        await other.getByRole("button", { name: "同步并退出" }).click();
        await other.getByRole("heading", { name: "登录工作区" }).waitFor();
        await page.getByRole("heading", { name: "恢复工作区会话" }).waitFor();
        assert.equal(await page.locator("[inert] input").inputValue(), "标签页甲草稿");
    }

    if (scenario === "partial-bootstrap") {
        await login("partial");
        await page.getByRole("heading", { name: "恢复工作区会话" }).waitFor();
        assert.equal(await page.getByLabel("原账号").inputValue(), "partial");
        assert.equal(await page.getByLabel("用户名", { exact: true }).count(), 0);
        await page.getByRole("button", { name: "放弃本地未同步内容并切换账号" }).click();
        await page.getByRole("button", { name: "确认放弃并退出" }).click();
        await page.getByRole("heading", { name: "登录工作区" }).waitFor();
        await login("bravo");
        await page.getByRole("heading", { name: "创作工作区" }).waitFor();
        assert.equal(await page.evaluate(() => window.platformTest.hydrations), 1);
        assert.equal(await page.evaluate(() => window.platformTest.clears), 1);
    }

    if (scenario === "session-monitor") {
        await login();
        await page.getByLabel("本地草稿").fill("服务端撤销前的草稿");
        await page.evaluate(() => {
            Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
            document.dispatchEvent(new Event("visibilitychange"));
        });
        const checksBefore = await page.evaluate(async () => (await (await fetch("/test/auth-checks")).json()).data.count);
        await page.waitForTimeout(2_700);
        const checksAfter = await page.evaluate(async () => (await (await fetch("/test/auth-checks")).json()).data.count);
        assert.equal(checksAfter, checksBefore);
        await page.evaluate(async () => { await fetch("/test/expire", { method: "POST" }); });
        const started = Date.now();
        await page.evaluate(() => {
            Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
            document.dispatchEvent(new Event("visibilitychange"));
        });
        await page.getByRole("heading", { name: "恢复工作区会话" }).waitFor();
        assert.ok(Date.now() - started < 5_000);
        assert.equal(await page.locator("[inert] input").inputValue(), "服务端撤销前的草稿");
    }
} finally {
    await context.close();
    await browser.close();
}
