import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";

const [url, scenario] = process.argv.slice(2);
const executablePath = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, process.env.CHROME_PATH, "C:/Program Files/Google/Chrome/Application/chrome.exe"].find((path) => path && existsSync(path));
const browser = await chromium.launch({ executablePath, headless: true, timeout: 10_000 });
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
page.setDefaultTimeout(8_000);
page.on("pageerror", (error) => console.error(error));
const state = async () => page.evaluate(async () => (await (await fetch("/test/state")).json()).data);
const menu = async (username, action) => {
    await page.getByRole("button", { name: `管理 ${username}`, exact: true }).click();
    await page.getByRole("menuitem", { name: action, exact: true }).click();
};

const assertReadable = async (selectors) => {
    const samples = await page.evaluate((selectors) => {
        const canvas = document.createElement("canvas");
        canvas.width = canvas.height = 1;
        const context = canvas.getContext("2d");
        const rgba = (value) => {
            context.clearRect(0, 0, 1, 1);
            context.fillStyle = value;
            context.fillRect(0, 0, 1, 1);
            return Array.from(context.getImageData(0, 0, 1, 1).data);
        };
        const blend = (front, back) => front.slice(0, 3).map((channel, index) => channel * front[3] / 255 + back[index] * (1 - front[3] / 255));
        const luminance = (color) => color.map((channel) => channel / 255).map((channel) => channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4).reduce((total, value, index) => total + value * [0.2126, 0.7152, 0.0722][index], 0);
        return selectors.map((selector) => {
            const element = document.querySelector(selector);
            if (!element) throw new Error(`缺少可读性检查目标：${selector}`);
            const ancestors = [];
            for (let current = element; current; current = current.parentElement) ancestors.unshift(current);
            const background = ancestors.reduce((color, current) => blend(rgba(getComputedStyle(current).backgroundColor), color), [255, 255, 255]);
            const foreground = blend(rgba(getComputedStyle(element).color), background);
            const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
            return { selector, contrast: (lighter + 0.05) / (darker + 0.05) };
        });
    }, selectors);
    for (const sample of samples) assert.ok(sample.contrast >= 4.5, `${sample.selector} 对比度 ${sample.contrast.toFixed(2)} 低于 4.5`);
};

try {
    await page.goto(url + (scenario === "guard" ? "?role=user" : scenario === "audit" ? "?theme=dark" : ""));
    const before = await state();
    if (scenario === "guard") {
        await page.getByText("创作工作区", { exact: true }).waitFor();
        assert.equal((await state()).adminReads, 0);
    } else {
        await page.getByRole("button", { name: "管理 manager", exact: true }).waitFor();
    }
    if (scenario === "create") {
        await page.getByRole("button", { name: "创建账号", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await dialog.getByLabel("用户名", { exact: true }).fill("bravo");
        await dialog.getByLabel("显示名", { exact: true }).fill("新用户");
        await dialog.getByLabel("初始密码", { exact: true }).fill("password-long-enough");
        await dialog.getByRole("button", { name: "创建账号", exact: true }).click();
        await dialog.getByText("请输入不小于 0 的安全整数").first().waitFor();
        assert.equal((await state()).writes.length, before.writes.length);
        await dialog.getByLabel("初始 AI 点数", { exact: true }).fill("-1");
        await dialog.getByLabel("存储上限（字节）", { exact: true }).fill("0");
        await dialog.getByRole("button", { name: "创建账号", exact: true }).click();
        await dialog.getByText("请输入不小于 0 的安全整数").first().waitFor();
        assert.equal(await dialog.getByLabel("初始 AI 点数", { exact: true }).inputValue(), "-1");
        assert.equal((await state()).writes.length, before.writes.length);
        await dialog.getByLabel("初始 AI 点数", { exact: true }).fill("0");
        await dialog.getByLabel("存储上限（字节）", { exact: true }).fill("0");
        await dialog.getByLabel("存储上限（字节）", { exact: true }).blur();
        await dialog.getByText("请输入不小于 0 的安全整数").first().waitFor({ state: "hidden" });
        const width = await dialog.locator(".platform-admin-number").first().evaluate((element) => ({ control: element.getBoundingClientRect().width, container: element.parentElement.getBoundingClientRect().width }));
        assert.ok(width.control >= width.container - 1);
        await assertReadable([".platform-admin-dialog .ant-form-item-extra", ".platform-admin-dialog .ant-form-item-label label"]);
        await dialog.getByText("必须明确填写；0 表示不允许新增存储。1 GiB = 1,073,741,824 字节。", { exact: true }).scrollIntoViewIfNeeded();
        await mkdir("../.local", { recursive: true });
        await page.screenshot({ path: "../.local/platform-admin-create.png", fullPage: true });
        await dialog.getByRole("button", { name: "创建账号", exact: true }).click();
        await page.getByRole("button", { name: "管理 bravo", exact: true }).waitFor();
        const created = (await state()).writes.at(-1);
        assert.deepEqual(created.body, { username: "bravo", displayName: "新用户", password: "password-long-enough", role: "user", initialPoints: 0, storageLimitBytes: 0 });
    }
    if (scenario === "manage") {
        await menu("alice", "编辑账号");
        let dialog = page.getByRole("dialog");
        assert.equal(await dialog.getByLabel("用户名", { exact: true }).count(), 0);
        await dialog.getByLabel("显示名", { exact: true }).fill("改名用户");
        await dialog.getByRole("button", { name: /^保\s*存$/ }).click();
        await page.getByText("改名用户", { exact: true }).waitFor();
        assert.deepEqual((await state()).writes.at(-1).body, { displayName: "改名用户", role: "user" });
        await menu("alice", "查看与修改额度");
        dialog = page.getByRole("dialog");
        await dialog.getByText("23", { exact: true }).waitFor();
        await dialog.getByLabel("存储上限（字节）", { exact: true }).fill("0");
        await dialog.getByRole("button", { name: /^保\s*存$/ }).click();
        await dialog.waitFor({ state: "hidden" });
        assert.deepEqual((await state()).writes.at(-1).body, { limitBytes: 0 });
        await menu("alice", "查看与修改额度");
        dialog = page.getByRole("dialog");
        await dialog.getByLabel("存储上限（字节）", { exact: true }).waitFor();
        assert.equal(await dialog.getByLabel("存储上限（字节）", { exact: true }).inputValue(), "0");
        await dialog.getByRole("button", { name: /^取\s*消$/ }).click();
        await dialog.waitFor({ state: "hidden" });
        await menu("alice", "重置密码");
        dialog = page.getByRole("dialog");
        await dialog.getByLabel("新密码", { exact: true }).fill("new-password-long-enough");
        await dialog.getByRole("button", { name: /^保\s*存$/ }).click();
        await dialog.waitFor({ state: "hidden" });
        assert.deepEqual((await state()).writes.at(-1).body, { password: "new-password-long-enough" });
        await menu("alice", "禁用账号");
        await page.getByRole("dialog").getByRole("button", { name: /^保\s*存$/ }).click();
        await page.getByText("已禁用 · 待改密", { exact: true }).waitFor();
        await menu("alice", "注销账号");
        await page.getByRole("dialog").getByRole("button", { name: "确认注销", exact: true }).click();
        await page.getByText("已注销", { exact: true }).waitFor();
        assert.equal(await page.getByRole("button", { name: "管理 alice", exact: true }).count(), 0);
        await page.getByRole("button", { name: "管理 manager", exact: true }).click();
        assert.equal(await page.getByRole("menuitem", { name: "禁用账号", exact: true }).count(), 0);
        assert.equal(await page.getByRole("menuitem", { name: "注销账号", exact: true }).count(), 0);
    }
    if (scenario === "audit") {
        await page.getByRole("button", { name: "管理审计", exact: true }).click();
        await page.getByText("存储上限（字节）：1,073,741,824 → 0", { exact: true }).waitFor();
        await page.setViewportSize({ width: 390, height: 844 });
        await page.getByLabel("筛选对象账号 ID").waitFor();
        assert.equal(await page.evaluate(() => document.compatMode), "CSS1Compat");
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
        await assertReadable([".platform-admin-tabs .ant-btn", ".ant-table-thead .ant-table-cell", ".ant-table-tbody .ant-table-cell", ".admin-pagination-total"]);
        const searchIcon = await page.locator(".ant-input-search .anticon-search svg").boundingBox();
        assert.ok(searchIcon && searchIcon.width >= 12 && searchIcon.height >= 12);
        assert.equal(await page.getByRole("button", { name: "管理审计", exact: true }).getAttribute("aria-pressed"), "true");
        await mkdir("../.local", { recursive: true });
        await page.screenshot({ path: "../.local/platform-admin-audit-mobile.png", fullPage: true });
    }
} finally {
    await browser.close();
}
