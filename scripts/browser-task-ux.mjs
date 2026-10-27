import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";

// This script talks to the isolated Go HTTP harness. It never intercepts API
// responses and the harness does not dispatch a cloud write Worker.
const url = process.argv[2];
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.NARBE_BROWSER_EXECUTABLE ||
    (process.platform === "darwin"
      ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
      : undefined),
});
const page = await browser.newPage();
const errors = [],
  jobs = [],
  confirmations = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("request", (r) => {
  if (r.method() !== "POST") return;
  if (r.url().endsWith("/api/jobs")) jobs.push(r.postDataJSON());
  if (r.url().endsWith("/api/confirm")) confirmations.push(r.postDataJSON());
});
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
const password = "Fixture-Password-UX-M9!";
async function shot(name) {
  await page.screenshot({ path: `${dir}/task-ux-${name}.png`, fullPage: true });
  assert.equal(
    await page.locator("body").evaluate((el) => el.scrollWidth > innerWidth),
    false,
    "page must fit its viewport",
  );
}
async function instances() {
  await page.locator('[data-nav="instances"]').click();
  await page.locator('[data-details="0"]').waitFor();
  const actions = await page
    .locator('[data-action-select="0"] option')
    .evaluateAll((options) => options.map((option) => option.value));
  assert.deepEqual(
    [...actions].sort(),
    ["start", "stop", "reboot", "delete"].sort(),
    "live status action hints must not hide API-supported operations",
  );
}
async function submit(button, action) {
  const before = jobs.length,
    beforeConfirmation = confirmations.length;
  const response = page.waitForResponse(
    (r) => r.url().endsWith("/api/jobs") && r.request().method() === "POST",
  );
  await button.click();
  const r = await response;
  assert.ok([200, 202].includes(r.status()), await r.text());
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  assert.equal(
    jobs.length,
    before + 1,
    "one click must submit exactly one job",
  );
  assert.equal(
    confirmations.length,
    beforeConfirmation + 1,
    "one fresh operation must obtain one backend token",
  );
  const body = jobs.at(-1);
  assert.equal(body.action, action);
  assert.equal(body.id, confirmations.at(-1).id);
  assert.ok(body.confirmation);
  assert.equal(body.networkRevision, 0);
  assert.ok(body.accountRevision > 0);
  const cancel = page.locator(`[data-cancel="${body.id}"]`);
  await cancel.waitFor();
  const cancelled = page.waitForResponse((reply) =>
    reply.url().endsWith(`/api/jobs/${body.id}/cancel`),
  );
  await cancel.click();
  assert.equal((await cancelled).status(), 200);
  return body;
}
async function toggle(form) {
  const input = form.locator('[name="password"]');
  assert.equal(await input.getAttribute("type"), "text");
  await input.fill(password);
  await form.getByRole("button", { name: "隐藏密码", exact: true }).click();
  assert.equal(await input.getAttribute("type"), "password");
  assert.equal(await input.inputValue(), password);
  await form.getByRole("button", { name: "显示密码", exact: true }).click();
  assert.equal(await input.getAttribute("type"), "text");
  assert.equal(await input.inputValue(), password);
}
async function advanced(action) {
  await instances();
  await page.locator('[data-details="0"]').click();
  await page.locator(`[data-advanced="${action}"]`).click();
  await page.locator(".advanced-form").waitFor();
  const form = page.locator(".advanced-form");
  assert.equal(await form.locator('[name="confirmName"]').count(), 0);
  assert.equal(await form.locator('[name="acknowledge"]').count(), 0);
  return form;
}
try {
  await page.goto(url);
  await page
    .locator('#login [name="password"]')
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator('#login button[type="submit"]').click();
  await page.locator('[data-details="0"]').waitFor();
  for (const [size, viewport] of [
    ["desktop", { width: 1440, height: 1080 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    await page.setViewportSize(viewport);
    await instances();
    await page.locator("#create").click();
    const create = page.locator("#create-form");
    await create.locator('[name="region"]').selectOption("r");
    await create
      .locator('[name="plan"] option[value="p"]')
      .waitFor({ state: "attached" });
    await create.locator('[name="plan"]').selectOption("p");
    await create.locator('[name="image"]').selectOption("im");
    await create.locator('[name="name"]').fill(`ux-${size}`);
    assert.equal(await create.locator('[name="confirmName"]').count(), 0);
    await toggle(create);
    assert.ok(
      !(await create.locator("#create-summary").innerText()).includes(password),
    );
    await shot(`create-${size}`);
    await submit(
      create.getByRole("button", { name: "创建服务器", exact: true }),
      "create",
    );

    for (const action of ["start", "stop", "reboot"]) {
      await instances();
      await page.locator('[data-action-select="0"]').selectOption(action);
      await submit(page.locator('[data-action="0"]'), action);
      assert.equal(await page.locator("dialog").evaluate((d) => d.open), false);
    }
    await instances();
    await page.locator('[data-action-select="0"]').selectOption("delete");
    const before = jobs.length;
    await page.locator('[data-action="0"]').click();
    await page.locator("dialog form").waitFor();
    assert.equal(
      jobs.length,
      before,
      "delete must wait for its explicit risk button",
    );
    assert.equal(await page.locator('dialog [name="confirmName"]').count(), 0);
    assert.match(await page.locator("dialog").innerText(), /删除无法撤销/);
    await shot(`delete-${size}`);
    await submit(page.locator('dialog button[type="submit"]'), "delete");

    let form = await advanced("rebuild");
    await form
      .locator('[name="newImage"] option[value="im"]')
      .waitFor({ state: "attached" });
    await form.locator('[name="newImage"]').selectOption("im");
    await toggle(form);
    assert.match(await form.innerText(), /重装会清除所有现有磁盘/);
    assert.equal(
      await form.locator('[name="metadataAcknowledged"]').count(),
      0,
    );
    await shot(`rebuild-${size}`);
    const rebuilt = await submit(
      form.locator('button[type="submit"]'),
      "rebuild",
    );
    assert.equal(rebuilt.advanced.metadataAcknowledged, true);
    assert.equal(rebuilt.advanced.acknowledge, true);

    form = await advanced("password-reset");
    await form.locator('[name="newDisk"]').selectOption("42");
    await toggle(form);
    assert.match(await form.innerText(), /按需关机、重置root密码并确认事件/);
    assert.ok(!(await form.innerText()).includes("需要服务器完全关机"));
    await shot(`password-reset-${size}`);
    const reset = await submit(
      form.locator('button[type="submit"]'),
      "password-reset",
    );
    assert.equal(reset.target, "123");
    assert.equal(reset.advanced.diskId, 42);
    assert.equal(reset.advanced.password, password);
    const publicJobs = await page.evaluate(async () =>
      (await fetch("/api/jobs")).text(),
    );
    assert.ok(
      !publicJobs.includes(password),
      "password must not be returned in task data",
    );
    const storage = await page.evaluate(() =>
      JSON.stringify([
        Object.entries(localStorage),
        Object.entries(sessionStorage),
      ]),
    );
    assert.ok(
      !storage.includes(password),
      "password must not enter browser storage",
    );
    const unknown = page.locator(".task-row").filter({
      has: page.locator('[data-resolve="task-ux-unknown-fixture"]'),
    });
    await unknown.waitFor();
    assert.match(await unknown.innerText(), /正在重置密码/);
    assert.match(await unknown.innerText(), /后续风险步骤已停止/);
    await shot(`tasks-${size}`);
  }
  // Unknown resolution needs an administrator and a clear attestation, never a name copy.
  await page.locator('[data-resolve="task-ux-unknown-fixture"]').click();
  assert.equal(await page.locator('dialog [name="confirmName"]').count(), 0);
  await page
    .locator('dialog [name="password"]')
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator('dialog [name="checked"]').check();
  const resolved = page.waitForResponse((r) =>
    r.url().endsWith("/api/jobs/task-ux-unknown-fixture/resolve"),
  );
  await page.locator('dialog button[type="submit"]').click();
  assert.equal((await resolved).status(), 200);
  await page.locator('[data-nav="accounts"]').click();
  await page.locator("[data-remove]").first().click();
  assert.equal(await page.locator('dialog [name="confirmName"]').count(), 0);
  assert.equal(
    await page.locator('dialog [name="password"]').getAttribute("type"),
    "password",
  );
  await page
    .locator('dialog [name="password"]')
    .fill(process.env.NARBE_TEST_PASSWORD);
  const removed = page.waitForResponse(
    (r) =>
      r.request().method() === "DELETE" && r.url().includes("/api/accounts/"),
  );
  await page.locator('dialog button[type="submit"]').click();
  assert.equal((await removed).status(), 200);
  assert.equal(new Set(jobs.map((job) => job.id)).size, jobs.length);
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      httpFixture: "real Go Handler/API/SQLite",
      jobs: jobs.length,
      desktopAndMobile: true,
      directOrdinaryActions: true,
      oneExplicitDangerConfirmation: true,
      cloudPasswordsVisibleWithToggle: true,
      plaintextPasswordResults: false,
      browserStoredPasswords: false,
      realCloudWrites: 0,
    }),
  );
} finally {
  await browser.close();
}
