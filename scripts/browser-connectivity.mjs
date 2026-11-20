import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.NARBE_BROWSER_EXECUTABLE ||
    (process.platform === "darwin"
      ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
      : undefined),
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1080 } });
const errors = [],
  submissions = [];
let probeReads = 0;
page.on("pageerror", (e) => errors.push(e.message));
page.on("request", (r) => {
  if (r.url().endsWith("/api/probes") && r.method() === "GET") probeReads++;
  if (r.url().endsWith("/api/jobs") && r.method() === "POST")
    submissions.push(r.postDataJSON());
});
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
async function shot(name) {
  await page.waitForFunction(
    () => !document.querySelector("#toast")?.textContent,
  );
  for (const [device, size] of [
    ["desktop", { width: 1440, height: 1080 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    await page.setViewportSize(size);
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
      false,
      "page horizontal overflow",
    );
    const inDialog = await page.locator("#dialog").evaluate((d) => d.open);
    if (inDialog)
      await page.locator("[name=screeningRisk]").scrollIntoViewIfNeeded();
    const surface = inDialog ? page.locator("#dialog") : page;
    await surface.screenshot({
      path: `${dir}/cn-connectivity-${name}-${device}.png`,
      ...(inDialog ? {} : { fullPage: true }),
    });
  }
}
async function create(name, waitInventory = true) {
  await page.locator("[data-nav=instances]").click();
  if (waitInventory) await page.locator("#empty-create").waitFor();
  else
    await page
      .getByText("正在查询云端资源，数量尚未确认…", { exact: true })
      .waitFor();
  await page.locator("#create").click();
  const f = page.locator("#create-form");
  await f.locator("[name=name]").fill(name);
  await f
    .locator('[name=region] option[value="r"]')
    .waitFor({ state: "attached", timeout: 5000 });
  await f.locator("[name=region]").selectOption("r");
  await f
    .locator('[name=plan] option[value="p"]')
    .waitFor({ state: "attached" });
  await f.locator("[name=plan]").selectOption("p");
  await f
    .locator('[name=image] option[value="im"]')
    .waitFor({ state: "attached" });
  await f.locator("[name=image]").selectOption("im");
  await f.locator("[name=key]").selectOption("key");
  return f;
}
try {
  await page.goto(process.argv[2]);
  await page
    .locator("#login [name=password]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator("#login button[type=submit]").click();
  await page.locator("[data-nav=connectivity]").waitFor();
  let f = await create("ordinary-unchanged", false);
  assert.equal(await f.locator("[name=screeningEnabled]").isChecked(), false);
  assert.equal(await f.locator("[data-screening-fields]").isHidden(), true);
  await f.locator("button[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  assert.ok(!("screening" in submissions.at(-1)));
  assert.equal(
    probeReads,
    0,
    "ordinary create must not read probes while screening is disabled",
  );

  for (const protocol of ["tcp", "tls", "http"]) {
    f = await create("screening-" + protocol);
    await f.locator("[name=screeningEnabled]").check();
    await f
      .locator('[name=screeningProbe] option[value="cn-probe"]')
      .waitFor({ state: "attached" });
    await f.locator("[name=screeningProbe]").selectOption("cn-probe");
    await f.locator("[name=screeningProtocol]").selectOption(protocol);
    await f
      .locator("[name=screeningPort]")
      .fill(protocol === "tcp" ? "22" : protocol === "tls" ? "443" : "80");
    if (protocol === "tls")
      await f.locator("[name=screeningServerName]").fill("health.example.com");
    if (protocol === "http")
      await f.locator("[name=screeningPath]").fill("/ready");
    await f.locator("button[type=submit]").click();
    await page
      .getByRole("heading", { name: "操作任务", exact: true })
      .waitFor();
    const screening = submissions.at(-1).screening;
    assert.equal(screening.protocol, protocol);
    assert.equal(screening.expectedStatus, protocol === "http" ? 200 : 0);
    assert.equal(screening.path, protocol === "http" ? "/ready" : "");
    assert.equal(screening.challenge, "");
    assert.equal(screening.autoReplace, false);
    assert.equal(screening.maxAttempts, 1);
    assert.equal(screening.riskAccepted, false);
  }

  f = await create("screening-owned-only");
  await f.locator("[name=screeningEnabled]").check();
  for (const [field, max] of [
    ["screeningStatus", "299"],
    ["screeningTimeout", "15"],
    ["screeningReady", "1800"],
    ["screeningDuration", "21600"],
  ])
    assert.equal(await f.locator(`[name=${field}]`).getAttribute("max"), max);
  await f
    .locator('[name=screeningProbe] option[value="cn-probe"]')
    .waitFor({ state: "attached" });
  await f.locator("[name=screeningProbe]").selectOption("offline-probe");
  assert.match(
    await f.locator("[data-screening-probe-status]").innerText(),
    /离线或出口未确认.*US/,
  );
  await f.locator("[name=screeningProbe]").selectOption("cn-probe");
  await f.locator("[name=screeningControl]").selectOption("control-probe");
  await f.locator("[name=screeningProtocol]").selectOption("https");
  await f.locator("[name=screeningPort]").fill("443");
  await f.locator("[name=screeningServerName]").fill("health.example.com");
  await f.locator("[name=screeningPath]").fill("/health");
  await f
    .locator("[name=screeningChallenge]")
    .fill("one-use-current-instance-challenge");
  await f.locator("[name=screeningReplace]").check();
  assert.match(
    await f.locator("[data-screening-replacement]").innerText(),
    /估算不是云厂商账单/,
  );
  assert.equal(await f.locator("[name=screeningRisk]").isChecked(), false);
  await shot("create-risk");
  await f.locator("button[type=submit]").click();
  assert.equal(
    submissions.length,
    4,
    "risk must be acknowledged once before auto replacement",
  );
  await f.locator("[name=screeningRisk]").check();
  await f.locator("button[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  assert.equal(submissions.length, 5);
  assert.deepEqual(submissions.at(-1).screening, {
    probeId: "cn-probe",
    controlProbeId: "control-probe",
    protocol: "https",
    port: 443,
    serverName: "health.example.com",
    path: "/health",
    expectedStatus: 200,
    challenge: "one-use-current-instance-challenge",
    timeoutSeconds: 5,
    intervalSeconds: 10,
    successes: 3,
    failures: 3,
    readySeconds: 300,
    maxDurationSeconds: 1800,
    maxAttempts: 5,
    autoReplace: true,
    budgetUSD: 5,
    costPerAttemptUSD: 1,
    riskAccepted: true,
  });
  await page.locator("[data-nav=connectivity]").click();
  await page
    .getByText("检测记录暂时不可用（HTTP测试替身）", { exact: false })
    .waitFor();
  assert.equal(await page.locator("[data-screening-list] .loading").count(), 0);
  await page.locator("[data-connectivity-refresh]").click();
  await page.locator("[data-screening-id=screening-active]").waitFor();
  assert.match(await page.locator("#content").innerText(), /尚无有效检测目标/);
  assert.match(
    await page.locator("#content").innerText(),
    /删除结果未知；不会创建下一台收费实例/,
  );
  assert.match(
    await page.locator("#content").innerText(),
    /实际出口.*198\.51\.100\.44/,
  );
  assert.match(
    await page.locator("#content").innerText(),
    /离线 \/ 出口待核实/,
  );
  const active = page.locator("[data-screening-id=screening-active]");
  await active.locator(":scope > .screening-history > summary").click();
  assert.match(await active.innerText(), /再次分配到相同 IP/);
  assert.match(await active.innerText(), /检测超时/);
  assert.match(await active.innerText(), /instance-exact-123/);
  const unknown = page.locator("[data-screening-id=screening-unknown]");
  await unknown.locator(":scope > .screening-history > summary").click();
  const ledger = unknown.locator(".screening-operation-ledger");
  assert.equal(
    await ledger.getAttribute("open"),
    null,
    "round ledgers start collapsed",
  );
  await ledger.locator("summary").click();
  assert.match(await ledger.innerText(), /azure-old-pip/);
  assert.match(await ledger.innerText(), /azure-new-pip/);
  assert.match(await ledger.innerText(), /azure-partial-owned-disk/);
  assert.match(await ledger.innerText(), /deleted-old-vm/);
  assert.match(await ledger.innerText(), /已确认删除/);
  assert.match(await ledger.innerText(), /存在及费用待核实/);
  assert.equal(
    await ledger.locator("[data-round-resource=azure-old-pip]").count(),
    1,
  );
  assert.equal(
    await ledger.locator("[data-round-resource=azure-new-pip]").count(),
    1,
  );
  assert.ok(
    !(await unknown.innerText()).includes("never-render-ledger-secret"),
  );
  await shot("workflow-evidence");
  await page.locator("[data-screening-reconcile=screening-unknown]").click();
  await active.locator("[data-screening-stop]").click();
  await page.getByText("已停止；实例仍存在", { exact: true }).waitFor();
  assert.equal(
    await page
      .locator("[data-screening-id=screening-active] [data-screening-stop]")
      .count(),
    0,
  );
  assert.match(
    await page.locator("[data-screening-id=screening-active]").innerText(),
    /仍存在并继续计费/,
  );
  await shot("stopped-resource-ledger");
  await page.locator(".probe-register summary").click();
  const registration = page.locator("#probe-registration");
  await registration.locator("[name=probeName]").fill("只展示一次的探针");
  await registration.locator("[name=probeIP]").fill("198.51.100.46");
  await registration.locator("button[type=submit]").click();
  await page.locator("[data-probe-token]").waitFor();
  assert.equal(
    await page.locator("[data-probe-token]").inputValue(),
    "fixture-once-only-registration-token",
  );
  const persisted = await page.evaluate(() =>
    JSON.stringify({
      local: { ...localStorage },
      session: { ...sessionStorage },
    }),
  );
  assert.ok(
    !persisted.includes("registration-token") &&
      !persisted.includes("one-use-current-instance"),
  );
  await page.locator("[data-probe-clear]").click();
  assert.equal(await page.locator("[data-probe-token]").count(), 0);
  await page.locator("[data-probe-disable=cn-probe]").click();
  await page.getByText("已停用", { exact: true }).waitFor();
  await shot("probe-disabled");
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      httpGoFixture: true,
      desktopMobile: true,
      creates: submissions.length,
      realCloudWrites: 0,
      stoppedResourcesVisible: true,
      registrationTokenPersisted: false,
    }),
  );
} catch (e) {
  await page.screenshot({
    path: `${dir}/cn-connectivity-failure.png`,
    fullPage: true,
  });
  throw e;
} finally {
  await browser.close();
}
