import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";
const url = process.argv[2],
  aid = process.argv[3];
const browser = await chromium.launch({
  executablePath:
    process.env.NARBE_BROWSER_EXECUTABLE ||
    (process.platform === "darwin"
      ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
      : undefined),
  headless: true,
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1080 } });
const errors = [],
  confirms = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("dialog", (d) => d.accept());
page.on("request", (r) => {
  if (r.url().endsWith("/api/confirm") && r.method() === "POST")
    confirms.push(r.postDataJSON());
});
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
const scenario = async (value) => {
  const r = await page.request.get(url + "/__firewall-fixture?case=" + value);
  assert.equal(r.status(), 204);
};
const openAccount = async () => {
  await page.goto(url);
  await page.locator("[data-nav=accounts]").click();
  await page.locator("[data-firewalls]").first().click();
};
const shot = async (name) => {
  await page.screenshot({ path: `${dir}/firewall-${name}-desktop.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: `${dir}/firewall-${name}-mobile.png` });
  await page.setViewportSize({ width: 1440, height: 1080 });
};
try {
  await page.goto(url);
  await page
    .locator("#login [name=password]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator("#login button[type=submit]").click();
  await page.locator("[data-nav=accounts]").waitFor();
  await scenario("null");
  await openAccount();
  await page
    .getByText("0个绑定：未绑定", { exact: true })
    .waitFor({ timeout: 3000 });
  await shot("null-fixed");

  // Editing preserves DROP even when both rule lists are null.
  await page.locator('[data-rule-edit="7"]').click();
  assert.equal(await page.locator("[name=inPolicy]").inputValue(), "DROP");
  assert.equal(await page.locator(".firewall-rule-row").count(), 0);
  await shot("rules-empty");
  const cancelPending = async () => {
    const reply = page.waitForResponse(
      (r) =>
        /\/api\/jobs\/[^/]+\/cancel$/.test(r.url()) &&
        r.request().method() === "POST",
    );
    await page.locator("[data-cancel]").first().click();
    assert.equal((await reply).status(), 200);
  };
  const submit = async (form, name) => {
    assert.equal(await form.locator("[name=confirmName]").count(), 0);
    const reply = page.waitForResponse(
      (r) => r.url().endsWith("/api/jobs") && r.request().method() === "POST",
    );
    await form.locator("button[type=submit]").click();
    const r = await reply;
    assert.equal(r.status(), 202, await r.text());
    await page
      .getByRole("heading", { name: "操作任务", exact: true })
      .waitFor();
    await cancelPending();
  };
  await submit(page.locator(".firewall-form"), "empty-fw");
  assert.deepEqual(confirms.at(-1).advanced.rules, {
    inbound: [],
    outbound: [],
    inbound_policy: "DROP",
    outbound_policy: "ACCEPT",
  });
  assert.equal(confirms.at(-1).advanced.snapshot, "fw-snapshot");
  await scenario("arrays");
  await openAccount();
  await page.getByText("0个绑定：未绑定", { exact: true }).waitFor();
  for (const only of ["inbound-null", "outbound-null"]) {
    await scenario(only);
    await openAccount();
    await page.locator('[data-rule-edit="7"]').click();
    assert.equal(await page.locator(".firewall-rule-row").count(), 0);
    assert.equal(await page.locator("[name=inPolicy]").inputValue(), "DROP");
  }
  await scenario("multiple");
  await openAccount();
  await page.getByText("2个绑定：", { exact: false }).waitFor();
  assert.ok(await page.locator('[data-delete-fw="7"]').isDisabled());
  await page.locator('[data-rule-edit="7"]').click();
  assert.match(await page.locator(".firewall-form").innerText(), /2 个绑定/);
  await shot("multiple-bindings");
  await scenario("empty");
  await openAccount();
  await page.getByText("暂无云防火墙", { exact: true }).waitFor();
  await shot("empty-list");
  await page.locator("[data-new-firewall]").click();
  await page.locator(".firewall-form [name=label]").fill("created-fw");
  await submit(page.locator(".firewall-form"), "created-fw");
  assert.equal(confirms.at(-1).action, "firewall-create");
  for (const bad of [
    "bad-policy",
    "missing-inbound",
    "missing-outbound",
    "missing-policy",
    "bad-devices",
    "bad-id",
    "bad-status",
    "bad-device",
    "bad-rule",
    "bad-rules",
    "bad-items",
  ]) {
    await scenario(bad);
    await openAccount();
    await page
      .getByText("防火墙数据不完整或格式无效", { exact: false })
      .waitFor();
    assert.equal(await page.locator(".firewall-card").count(), 0, bad);
  }
  await shot("malformed-blocked");
  const openCreate = async () => {
    await page.goto(url);
    await page.locator("#create").click();
    await page.locator("#create-form [name=region]").selectOption("r");
    await page
      .locator("#create-form [name=plan] option[value=p]")
      .waitFor({ state: "attached" });
    await page.locator("#create-form [name=plan]").selectOption("p");
    await page.locator("#create-form [name=image]").selectOption("im");
    await page.locator("#create-form [name=name]").fill("new-server");
    await page
      .locator("#create-form [name=password]")
      .fill("Fixture-Password-84!");
    assert.equal(
      await page.locator("#create-form [name=confirmName]").count(),
      0,
    );
  };
  await scenario("bad-policy");
  await openCreate();
  await page
    .locator("[data-firewall-error]")
    .getByText("防火墙数据不完整或格式无效", { exact: false })
    .waitFor();
  assert.ok(await page.locator("[data-unverified]").isHidden());
  const before = confirms.length;
  await page.locator("#create-form button[type=submit]").click();
  await page
    .locator("#create-form > .error-box")
    .getByText("防火墙数据不完整或格式无效", { exact: false })
    .waitFor();
  assert.equal(
    confirms.length,
    before,
    "malformed firewall became all-open submit",
  );
  await scenario("null");
  await openCreate();
  await page
    .locator('[name=firewallID] option[value="7"]')
    .waitFor({ state: "attached" });
  await page.locator("[name=firewallMode]").selectOption("existing");
  await page.locator("[name=firewallID]").selectOption("7");
  assert.match(await page.locator("[name=firewallID]").innerText(), /0个绑定/);
  await shot("create-existing");
  await page.locator("[name=firewallMode]").selectOption("all");
  const created = page.waitForResponse(
    (r) => r.url().endsWith("/api/jobs") && r.request().method() === "POST",
  );
  await page.locator("#create-form button[type=submit]").click();
  assert.equal((await created).status(), 202);
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  await cancelPending();
  assert.equal(confirms.at(-1).spec.firewall.mode, "all");
  assert.equal(confirms.at(-1).spec.scriptMode, "none");
  for (const kind of ["null", "interfaces"]) {
    await scenario(kind);
    await page.goto(url);
    await page.locator("[data-details]").first().click();
    await page.locator("[data-detail=firewall-bind]").click();
    await page
      .locator('[name=firewallID] option[value="7"]')
      .waitFor({ state: "attached" });
    await page.locator("[name=firewallMode]").selectOption("existing");
    await page.locator("[name=firewallID]").selectOption("7");
    if (kind === "interfaces")
      await page.locator("[name=newInterface]").selectOption("8");
    await shot("bind-" + kind);
    await submit(page.locator(".advanced-form"), "source");
    assert.equal(confirms.at(-1).advanced.bindingSnapshot, "binding-snapshot");
    assert.equal(
      confirms.at(-1).advanced.interfaceId,
      kind === "interfaces" ? 8 : 0,
    );
    assert.equal(confirms.at(-1).advanced.firewall.snapshot, "fw-snapshot");
  }
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      httpFixture: "real Go Handler/API/SQLite",
      nullCollections: true,
      malformedCases: 11,
      confirmations: confirms.length,
      realCloudWrites: 0,
      defaultAllUnchanged: true,
      legacyAndInterfaces: true,
    }),
  );
} catch (e) {
  await shot("failure");
  throw e;
} finally {
  await browser.close();
}
