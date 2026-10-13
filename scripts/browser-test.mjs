import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";
const url = process.argv[2];
const executablePath =
  process.env.NARBE_BROWSER_EXECUTABLE ||
  (process.platform === "darwin"
    ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
    : undefined);
const browser = await chromium.launch({ executablePath, headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const screenshotDir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(screenshotDir, { recursive: true });
try {
  await page.goto(url);
  await page
    .locator("#login input[name=password]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator("#login button[type=submit]").click();
  await page.getByRole("heading", { name: "先接入你的第一个云账号" }).waitFor();
  await page.getByRole("button", { name: "接入云账号 →" }).click();
  await page.locator("#account-form input[name=name]").fill("浏览器测试账号");
  await page
    .locator("#account-form select[name=provider]")
    .selectOption("digitalocean");
  await page
    .locator("#account-form input[name=token]")
    .fill("fixture-token-not-a-cloud-credential");
  await page.getByRole("button", { name: "验证并保存" }).click();
  await page.getByRole("heading", { name: "浏览器测试账号" }).waitFor();
  await page.getByRole("button", { name: "查看服务器" }).click();
  await page.locator("#region option[value=r]").waitFor({ state: "attached" });
  await page.locator("#region").selectOption("r");
  await page.getByRole("heading", { name: "这个地区还没有服务器" }).waitFor();
  await page.locator("#create").click();
  await page.locator("#create-form input[name=name]").fill("browser-server");
  await page
    .locator("#create-form select[name=plan] option[value=p]")
    .waitFor({ state: "attached" });
  await page.locator("#create-form select[name=plan]").selectOption("p");
  await page.locator("#create-form select[name=image]").selectOption("im");
  await page.locator("#create-form select[name=key]").selectOption("key");
  await page
    .locator("#create-form input[name=confirmName]")
    .fill("browser-server");
  await page.screenshot({
    path: screenshotDir + "/create-desktop.png",
    fullPage: true,
  });
  const submissionIds = [];
  let loseResponse = true;
  await page.route("**/api/jobs", async (route) => {
    if (route.request().method() !== "POST") {
      await route.continue();
      return;
    }
    submissionIds.push(route.request().postDataJSON().id);
    if (loseResponse) {
      loseResponse = false;
      await route.fetch();
      await route.abort("failed");
    } else await route.continue();
  });
  await page.locator("#create-form button[type=submit]").click();
  await page
    .getByText(
      "连接中断。请使用当前表单重试，系统会核实同一任务，避免重复操作。",
      { exact: true },
    )
    .waitFor();
  await page.locator("#create-form button[type=submit]").click();

  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  await page.getByText("已完成", { exact: true }).waitFor();
  await page.getByRole("button", { name: "服务器", exact: true }).click();
  await page.locator("#refresh").click();
  await page
    .locator("td .name")
    .filter({ hasText: "browser-server" })
    .waitFor();
  await page.screenshot({
    path: screenshotDir + "/servers-desktop.png",
    fullPage: true,
  });
  assert.equal(
    await page
      .locator("body")
      .evaluate((el) => el.scrollWidth > window.innerWidth),
    false,
    "desktop page overflow",
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: screenshotDir + "/servers-mobile.png",
    fullPage: true,
  });
  assert.equal(
    await page
      .locator("body")
      .evaluate((el) => el.scrollWidth > window.innerWidth),
    false,
    "mobile page overflow",
  );
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.locator('[data-action-select="0"]').selectOption("delete");
  await page.locator('[data-action="0"]').click();
  await page.locator("dialog input[name=confirmName]").fill("wrong-name");
  await page.locator("dialog button[type=submit]").click();
  await page
    .getByText("请输入准确的资源名称确认操作", { exact: true })
    .waitFor();
  assert.equal(
    await page.locator("dialog").evaluate((el) => el.open),
    true,
    "danger confirmation bypassed",
  );
  await page.locator("dialog input[name=confirmName]").fill("browser-server");
  await page.locator("dialog button[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  await page
    .getByRole("heading", { name: "删除服务器 · browser-server" })
    .waitFor();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page
    .locator("#password-form input[name=current]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page
    .locator("#password-form input[name=password]")
    .fill("different-fixture-password");
  await page.locator("#password-form input[name=confirm]").fill("mismatch");
  await page.locator("#password-form button[type=submit]").click();
  await page.getByText("两次输入的新密码不一致", { exact: true }).waitFor();
  await page.getByRole("button", { name: "退出登录 ↗" }).click();
  await page.getByRole("heading", { name: "登录工作台" }).waitFor();
  assert.equal(
    submissionIds[0],
    submissionIds[1],
    "retry changed the immutable intent ID",
  );
  assert.deepEqual(errors, [], "browser errors");
  console.log(
    "PASS: real browser + real Go API/SQLite + mock cloud: login/account/create/task/server/delete confirmation/settings/logout; desktop 1440x1000 and mobile 390x844; zero page errors.",
  );
} finally {
  await browser.close();
}
