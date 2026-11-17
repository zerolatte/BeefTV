import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { mkdir } from "node:fs/promises";
import { chromium } from "playwright";

const [url, scenario] = process.argv.slice(2);
const executablePath = [process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH, process.env.CHROME_PATH, "C:/Program Files/Google/Chrome/Application/chrome.exe"].find((path) => path && existsSync(path));
const browser = await chromium.launch({ executablePath, headless: true, timeout: 10_000 });
const page = await browser.newPage({ viewport: { width: 1280, height: 800 } });
page.setDefaultTimeout(8_000);
const failures = [];
page.on("pageerror", (error) => failures.push(error.message));
const state = async () => page.evaluate(async () => (await (await fetch("/test/state")).json()).data);
const openAdjustment = async () => {
    await page.getByRole("button", { name: "管理 alice", exact: true }).click();
    await page.getByRole("menuitem", { name: "调整 AI 点数", exact: true }).click();
    const dialog = page.getByRole("dialog");
    await dialog.getByRole("button", { name: "确认调整", exact: true }).waitFor();
    await page.waitForFunction(() => !document.querySelector('input[id="points"]')?.disabled);
    return dialog;
};
const submit = (dialog) => dialog.getByRole("button", { name: /确认调整$/ }).click();
const openReview = async () => {
    await page.getByRole("button", { name: "用量记录", exact: true }).click();
    await page.getByRole("cell", { name: "task-4", exact: true }).waitFor();
    assert.equal(await page.getByRole("button", { name: "核对任务 task-1", exact: true }).count(), 0);
    assert.equal(await page.getByRole("button", { name: "核对任务 task-5", exact: true }).count(), 0);
    await page.getByRole("button", { name: "核对任务 task-4", exact: true }).click();
    await page.getByRole("menuitem", { name: "人工核对", exact: true }).click();
    return page.getByRole("dialog");
};
const chooseReviewFact = async (dialog) => {
    assert.equal(await dialog.getByRole("radio").count(), 4);
    await dialog.getByRole("radio", { name: "完全失败但上游已消耗（结算并补偿）", exact: true }).check();
};
try {
    await page.goto(url + (scenario === "guard" ? "?role=user" : scenario === "usage" ? "?theme=dark" : ""));
    if (scenario === "guard") {
        await page.getByText("创作工作区", { exact: true }).waitFor();
        assert.deepEqual((await state()).reads, []);
        assert.deepEqual((await state()).writes, []);
    } else await page.getByRole("button", { name: "管理 manager", exact: true }).waitFor();
    if (scenario === "validation") {
        const dialog = await openAdjustment();
        await submit(dialog);
        await dialog.getByText("请填写 1—500 字的调整原因", { exact: true }).waitFor();
        await dialog.getByLabel("调整原因", { exact: true }).fill("测试发放");
        for (const value of ["0", "1.5", "9007199254740992"]) {
            await dialog.getByLabel("调整点数", { exact: true }).fill(value);
            await submit(dialog);
            await dialog.getByText("请输入非零安全整数", { exact: true }).waitFor();
            assert.equal((await state()).writes.length, 0);
        }
        await dialog.getByLabel("调整点数", { exact: true }).fill("5");
        await submit(dialog);
        await dialog.waitFor({ state: "hidden" });
        const result = await state();
        assert.equal(result.available, 28);
        assert.equal(result.writes[0].body.reason, "测试发放");
        assert.match(result.writes[0].body.clientOperationId, /^[\da-f-]{36}$/);
    }
    if (scenario === "retry") {
        let lost = false;
        await page.route("**/api/admin/users/user-1/point-adjustments", async (route) => {
            if (lost) return route.continue();
            lost = true;
            await route.fetch();
            await route.abort("connectionfailed");
        });
        const dialog = await openAdjustment();
        await dialog.getByLabel("调整点数", { exact: true }).fill("5");
        await dialog.getByLabel("调整原因", { exact: true }).fill("断网后重试");
        await submit(dialog);
        await dialog.getByRole("alert").waitFor();
        assert.equal((await state()).available, 28);
        await submit(dialog);
        await dialog.waitFor({ state: "hidden" });
        const result = await state();
        assert.equal(result.writes.length, 2);
        assert.deepEqual(result.writes[0].body, result.writes[1].body);
        assert.equal(result.available, 28);
        assert.equal(result.applied, 1);
        const reopened = await openAdjustment();
        await reopened.getByText("28", { exact: true }).waitFor();
    }
    if (scenario === "negative") {
        const dialog = await openAdjustment();
        await dialog.getByLabel("调整点数", { exact: true }).fill("-100");
        await dialog.getByLabel("调整原因", { exact: true }).fill("调整发放误差");
        await submit(dialog);
        await dialog.getByRole("alert").getByText("调整不能突破已结算与预留下限或整数上限", { exact: true }).waitFor();
        assert.equal(await dialog.getByLabel("调整点数", { exact: true }).inputValue(), "-100");
        await submit(dialog);
        await page.waitForFunction(async () => (await (await fetch("/test/state")).json()).data.writes.length === 2);
        await dialog.getByRole("button", { name: /确认调整$/ }).waitFor();
        await dialog.getByLabel("调整点数", { exact: true }).fill("-3");
        await submit(dialog);
        await dialog.waitFor({ state: "hidden" });
        const result = await state();
        assert.equal(result.writes[0].body.clientOperationId, result.writes[1].body.clientOperationId);
        assert.notEqual(result.writes[1].body.clientOperationId, result.writes[2].body.clientOperationId);
        assert.equal(result.available, 20);
    }
    if (scenario === "usage") {
        await page.getByRole("button", { name: "用量记录", exact: true }).click();
        await page.getByRole("cell", { name: "task-1", exact: true }).waitFor();
        for (const label of ["已预留", "提交中", "上游已受理", "结果待确认", "已结算", "已释放", "已补偿", "待核对"]) await page.getByRole("cell", { name: label, exact: true }).first().waitFor();
        assert.equal(await page.getByText("不应显示的上游提示词").count(), 0);
        assert.equal(await page.getByText("不应显示的生成内容").count(), 0);
        assert.equal(await page.locator(".ant-table-tbody tr[data-row-key]").count(), 20);
        await page.getByRole("button", { name: "下一页", exact: true }).click();
        await page.getByRole("cell", { name: "task-21", exact: true }).waitFor();
        assert.ok((await state()).reads.some((path) => path.includes("/usage?page=2&pageSize=20")));
        const input = page.getByLabel("筛选用量账号 ID", { exact: true });
        await input.fill("user-2"); await input.press("Enter");
        await page.getByText("1-1 / 共 1 条记录", { exact: true }).waitFor();
        assert.ok((await state()).reads.some((path) => path.includes("page=1&pageSize=20&accountId=user-2")));
        await input.fill("missing"); await input.press("Enter");
        await page.getByText("暂无用量记录", { exact: true }).waitFor();
        await input.fill("unavailable"); await input.press("Enter");
        await page.getByText("用量记录加载失败", { exact: true }).waitFor();
        assert.equal(await page.getByText("暂无用量记录", { exact: true }).count(), 0);
        await input.fill("user-1"); await input.press("Enter");
        await page.getByRole("cell", { name: "task-1", exact: true }).waitFor();
        await page.setViewportSize({ width: 390, height: 844 });
        assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1), false);
    }
    if (scenario === "review") {
        let lost = false;
        await page.route("**/api/admin/usage/intent-4/review", async (route) => {
            if (lost) return route.continue();
            lost = true;
            await route.fetch();
            await route.abort("connectionfailed");
        });
        const dialog = await openReview();
        await dialog.getByRole("button", { name: "确认核对", exact: true }).click();
        await dialog.getByText("请填写 1—500 字的核对依据", { exact: true }).waitFor();
        await chooseReviewFact(dialog);
        await dialog.getByLabel("核对依据", { exact: true }).fill("已核实上游完全失败且消耗计量，按固定点数补偿");
        await dialog.getByRole("button", { name: /确认核对$/ }).click();
        await dialog.getByText("请先确认已核实终局依据", { exact: true }).waitFor();
        assert.equal((await state()).writes.length, 0);
        await dialog.getByRole("checkbox").check();
        await dialog.getByRole("button", { name: /确认核对$/ }).click();
        await dialog.getByRole("alert").waitFor();
        await dialog.getByRole("button", { name: /确认核对$/ }).click();
        await dialog.waitFor({ state: "hidden" });
        const result = await state();
        assert.equal(result.reviews, 1);
        assert.deepEqual(result.writes[0].body, result.writes[1].body);
        assert.equal(result.writes[0].body.expectedState, "unknown");
        assert.equal(result.writes[0].body.fact, "failed_consumed");
        assert.equal("verified" in result.writes[0].body, false);
        await page.getByRole("row").filter({ has: page.getByRole("cell", { name: "task-4", exact: true }) }).getByRole("cell", { name: "已补偿", exact: true }).waitFor();
        assert.equal(await page.getByRole("button", { name: "核对任务 task-4", exact: true }).count(), 0);
    }
    if (scenario === "review-conflict") {
        const dialog = await openReview();
        await chooseReviewFact(dialog);
        await dialog.getByLabel("核对依据", { exact: true }).fill("核对状态冲突");
        await dialog.getByRole("checkbox").check();
        await dialog.getByRole("button", { name: /确认核对$/ }).click();
        await dialog.getByRole("alert").getByText("执行状态已经变化，请刷新后核对", { exact: true }).waitFor();
        assert.equal(await dialog.getByLabel("核对依据", { exact: true }).inputValue(), "核对状态冲突");
        await dialog.getByRole("button", { name: /确认核对$/ }).click();
        await page.waitForFunction(async () => (await (await fetch("/test/state")).json()).data.writes.length === 2);
        assert.deepEqual((await state()).writes[0].body, (await state()).writes[1].body);
        assert.equal((await state()).reviews, 0);
    }
    if (scenario === "reconciliation") {
        await page.getByRole("button", { name: "管理 alice", exact: true }).click();
        await page.getByRole("menuitem", { name: "查看与修改额度", exact: true }).click();
        const dialog = page.getByRole("dialog");
        await dialog.getByLabel("存储上限（字节）", { exact: true }).waitFor();
        assert.equal((await state()).reads.some((path) => path.includes("quota-reconciliation")), false);
        await dialog.getByRole("button", { name: "核对点数账本", exact: true }).click();
        await dialog.getByText("账本与额度账户不一致", { exact: true }).waitFor();
        await dialog.getByText("900719925474099312345", { exact: true }).waitFor();
        await dialog.getByRole("button", { name: /核对点数账本$/ }).click();
        await dialog.getByText("账本与额度账户一致", { exact: true }).waitFor();
        assert.equal(await dialog.getByText("900719925474099312345", { exact: true }).count(), 0);
        assert.equal((await state()).writes.length, 0);
    }
    await mkdir("../.local", { recursive: true });
    await page.screenshot({ path: `../.local/platform-quota-${scenario}.png`, fullPage: true, animations: "disabled" });
    assert.deepEqual(failures, []);
} catch (error) {
    await mkdir("../.local", { recursive: true });
    await page.screenshot({ path: `../.local/platform-quota-${scenario}-failure.png`, fullPage: true, animations: "disabled" });
    throw error;
} finally { await browser.close(); }
