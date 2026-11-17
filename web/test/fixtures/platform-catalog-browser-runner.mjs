import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";

const [url, scenario] = process.argv.slice(2);
const executablePath = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, process.env.CHROME_PATH, "C:/Program Files/Google/Chrome/Application/chrome.exe"].find((path) => path && existsSync(path));
const browser = await chromium.launch({ executablePath, headless: true, timeout: 10_000 });
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
page.setDefaultTimeout(8_000);
const failures = [];
page.on("pageerror", (error) => failures.push(error.message));
const state = async () => page.evaluate(async () => (await (await fetch("/test/state")).json()).data);
const menu = async (name, item) => {
    await page.getByRole("button", { name, exact: true }).click();
    await page.getByRole("menuitem", { name: item, exact: true }).click();
};
const select = async (label, text) => {
    await page.getByLabel(label, { exact: true }).click();
    await page.locator(".ant-select-dropdown:visible .ant-select-item-option-content").getByText(text, { exact: true }).click();
};
const save = async (dialog, name = /^保\s*存$/) => {
    await dialog.getByRole("button", { name, exact: true }).click();
    await dialog.waitFor({ state: "hidden" });
};
try {
    await page.goto(url + (scenario === "catalog" ? "?page=models&role=user" : ""));
    if (scenario === "catalog") {
        await page.getByText("text_generation · 3", { exact: true }).waitFor();
        await page.getByText("当前可查看模型与价格，生成暂未开放。", { exact: true }).waitFor();
        assert.deepEqual((await state()).reads, ["/api/models/catalog"]);
        assert.equal((await state()).writes.length, 0);
    } else await page.getByRole("button", { name: "管理 manager", exact: true }).waitFor();
    if (scenario === "channels") {
        await page.getByRole("button", { name: "平台渠道", exact: true }).click();
        await page.getByRole("button", { name: "创建渠道", exact: true }).click();
        let dialog = page.getByRole("dialog");
        await dialog.getByLabel("渠道名称", { exact: true }).fill("新渠道");
        await dialog.getByLabel("上游地址", { exact: true }).fill("https://provider.invalid/v1");
        await dialog.getByLabel("API Key", { exact: true }).fill("fixture-secret-key");
        await save(dialog);
        assert.equal((await state()).writes.at(-1).body.credentials.apiKey, "fixture-secret-key");
        await menu("管理渠道 测试渠道", "编辑与启停");
        dialog = page.getByRole("dialog");
        assert.equal(await dialog.getByLabel("API Key", { exact: true }).count(), 0);
        await dialog.getByRole("switch", { name: "启用渠道", exact: true }).click();
        await save(dialog);
        assert.equal((await state()).writes.at(-1).body.enabled, false);
        await menu("管理渠道 测试渠道", "轮换凭据");
        dialog = page.getByRole("dialog");
        assert.equal(await dialog.getByLabel("API Key", { exact: true }).inputValue(), "");
        await dialog.getByLabel("上游地址", { exact: true }).fill("https://next.invalid/v2");
        await dialog.getByLabel("API Key", { exact: true }).fill("next-fixture-key");
        await save(dialog, "保存新版本");
        assert.equal((await state()).writes.at(-1).path, "/api/admin/channels/channel-1/credentials");
        assert.equal(await page.getByText("next-fixture-key").count(), 0);
    }
    if (scenario === "channel-model") {
        await page.getByRole("button", { name: "平台渠道", exact: true }).click();
        await menu("管理渠道 测试渠道", "渠道模型");
        await page.getByRole("button", { name: "新增渠道模型", exact: true }).click();
        const dialog = page.getByRole("dialog", { name: "新增渠道模型", exact: true });
        await dialog.getByLabel("渠道模型标识", { exact: true }).fill("text-v1");
        await dialog.getByLabel("上游模型标识", { exact: true }).fill("upstream-text");
        await dialog.getByLabel("显示名", { exact: true }).fill("文本线路");
        await select("协议", "文本协议 · 文本");
        await save(dialog);
        const write = (await state()).writes.at(-1);
        assert.equal(write.body.capability, "text");
        assert.equal(write.body.protocol, "chat-completion");
        assert.equal("variantsJSON" in write.body, false);
        assert.equal("capabilityConfigJSON" in write.body, false);
        await page.getByText("channel-model-1", { exact: true }).waitFor();
    }
    if (scenario === "model") {
        await page.getByRole("button", { name: "逻辑模型", exact: true }).click();
        await menu("管理模型 平台模型 1", "发布新版本");
        let dialog = page.getByRole("dialog");
        await page.waitForFunction(() => document.querySelector('textarea[id="defaultOptionsJSON"]')?.value.includes("temperature"));
        await dialog.getByLabel("模型名称", { exact: true }).fill("新模型名称");
        assert.equal(await dialog.getByLabel("稳定模型标识", { exact: true }).isDisabled(), true);
        await save(dialog, /^发\s*布$/);
        const write = (await state()).writes.at(-1);
        assert.equal(write.body.expectedRevisionId, "revision-1");
        assert.deepEqual(write.body.defaultOptions, { temperature: 0.2 });
        assert.deepEqual(write.body.routes, [{ channelModelId: "channel-model-1", enabled: true, priority: 7, weight: 3 }]);
        await menu("管理模型 平台模型 1", "发布新版本");
        dialog = page.getByRole("dialog");
        await dialog.getByLabel("模型名称", { exact: true }).fill("冲突草稿");
        await dialog.getByRole("button", { name: /^发\s*布$/ }).click();
        await dialog.getByRole("alert").getByText("模型已更新，请刷新后再发布", { exact: true }).waitFor();
        assert.equal(await dialog.getByLabel("模型名称", { exact: true }).inputValue(), "冲突草稿");
        assert.equal(await dialog.isVisible(), true);
    }
    if (scenario === "prices") {
        await page.getByRole("button", { name: "逻辑模型", exact: true }).click();
        await menu("管理模型 平台模型 1", "固定点数价格");
        const dialog = page.getByRole("dialog");
        await select("模型操作", "text_generation");
        await dialog.getByLabel("固定点数", { exact: true }).fill("0");
        await dialog.getByRole("button", { name: /发布新价格$/ }).click();
        await dialog.getByText("请输入 1—9,007,199,254,740,991 的整数", { exact: true }).waitFor();
        assert.equal((await state()).writes.length, 0);
        await dialog.getByLabel("固定点数", { exact: true }).fill("7");
        await dialog.getByRole("button", { name: /发布新价格$/ }).click();
        await dialog.getByRole("cell", { name: "7", exact: true }).waitFor();
        assert.deepEqual((await state()).writes.at(-1).body, { operation: "text_generation", points: 7 });
        assert.equal(await dialog.locator(".ant-table-tbody tr[data-row-key]").count(), 2);
    }
    if (scenario === "grants") {
        await menu("管理 alice", "模型授权");
        let dialog = page.getByRole("dialog");
        await dialog.getByText("已选择 1 个模型", { exact: true }).waitFor();
        await dialog.getByRole("row").filter({ has: page.getByRole("cell", { name: "平台模型 1", exact: true }) }).getByRole("checkbox").check();
        await dialog.getByRole("button", { name: "下一页", exact: true }).click();
        const row = dialog.getByRole("row").filter({ has: page.getByRole("cell", { name: "平台模型 21", exact: true }) });
        assert.equal(await row.getByRole("checkbox").isChecked(), true);
        await save(dialog, "保存授权");
        assert.deepEqual((await state()).writes.at(-1).body.logicalModelIds.sort(), ["model-1", "model-21"]);
        await menu("管理 alice", "模型授权");
        dialog = page.getByRole("dialog");
        await dialog.getByText("已选择 2 个模型", { exact: true }).waitFor();
        await dialog.getByRole("button", { name: "清空授权", exact: true }).click();
        await save(dialog, "保存授权");
        assert.deepEqual((await state()).writes.at(-1).body, { logicalModelIds: [] });
    }
    await mkdir("../.local", { recursive: true });
    await page.screenshot({ path: `../.local/platform-catalog-${scenario}.png`, fullPage: true });
    assert.deepEqual(failures, []);
} catch (error) {
    await mkdir("../.local", { recursive: true });
    await page.screenshot({ path: `../.local/platform-catalog-${scenario}-failure.png`, fullPage: true });
    console.error(JSON.stringify({ failures }));
    throw error;
} finally { await browser.close(); }
