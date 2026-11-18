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
const errors = [];
const requests = { probes: 0, checks: 0, confirm: 0, jobs: 0 };
page.on("pageerror", (e) => errors.push(e.message));
page.on("request", (r) => {
  const url = new URL(r.url());
  if (url.pathname === "/api/probes" && r.method() === "GET") requests.probes++;
  if (url.pathname === "/api/ip/check") requests.checks++;
  if (url.pathname === "/api/confirm" && r.method() === "POST")
    requests.confirm++;
  if (url.pathname === "/api/jobs" && r.method() === "POST") requests.jobs++;
});
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });

async function screenshot(name) {
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
    const dialog = page.locator("#dialog");
    if (await dialog.evaluate((d) => d.open)) {
      assert.equal(
        await dialog.evaluate((d) => d.scrollWidth > d.clientWidth + 1),
        false,
        "dialog horizontal overflow",
      );
      await dialog.screenshot({
        path: `${dir}/ip-management-${name}-${device}.png`,
      });
    } else {
      await page.screenshot({
        path: `${dir}/ip-management-${name}-${device}.png`,
        fullPage: true,
      });
    }
  }
}

async function selectAccount(label) {
  await page.locator("#account").selectOption({ label });
  await page.locator("[data-ip-management]").first().waitFor();
}

try {
  await page.goto(process.argv[2]);
  await page
    .locator("#login [name=password]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator("#login button[type=submit]").click();
  await page.locator("[data-nav=instances]").waitFor();
  await page.locator("[data-ip-management]").first().waitFor();
  await selectAccount("DigitalOcean · digitalocean-fixture");
  const initialProbes = requests.probes;
  await page.locator("[data-ip-management]").first().click();
  const pane = page.locator(".ip-management");
  await pane.locator(".ip-addresses").waitFor();
  assert.match(await pane.innerText(), /原始公网 IPv4.*198\.51\.100\.10/s);
  assert.match(await pane.innerText(), /Reserved IP.*198\.51\.100\.20/s);
  assert.match(await pane.innerText(), /实际对外服务 IP.*198\.51\.100\.20/s);
  assert.match(await pane.innerText(), /Droplet 不支持原机更换原始公网 IPv4/);
  assert.match(await pane.innerText(), /32 ms/);
  assert.equal(
    await pane.locator("#ip-change-form button[type=submit]").count(),
    1,
  );
  assert.equal(
    await pane.locator("#ip-change-form button[type=submit]").isDisabled(),
    true,
  );
  assert.equal(
    requests.probes,
    initialProbes,
    "opening IP detail must not fetch probes",
  );
  await screenshot("do-addresses");
  await pane.locator("[data-ip-replacement]").click();
  await page.locator("#create-form").waitFor();
  assert.equal(
    await page.locator("#create-form [name=name]").inputValue(),
    "digitalocean-server-new",
  );
  assert.equal(
    await page.locator("#create-form [name=region]").inputValue(),
    "r",
  );
  await page.locator("#dialog [data-close]").first().click();

  await selectAccount("Linode · linode-fixture");
  await page.locator("[data-ip-management]").first().click();
  await pane.locator(".ip-addresses").waitFor();
  assert.match(await pane.innerText(), /配额、权限和网络接口代际/);
  assert.match(await pane.innerText(), /198\.51\.100\.30/);
  await pane.locator("[data-ip-check-open]").click();
  const form = pane.locator("#ip-check-form");
  await form
    .locator('[name=probeId] option[value="cn-probe"]')
    .waitFor({ state: "attached" });
  await form.locator("[name=probeId]").selectOption("cn-probe");
  await form.locator("[name=controlProbeId]").selectOption("control-probe");
  await form.locator("[name=protocol]").selectOption("http");
  await form.locator("[name=serverName]").fill("health.example.com");
  await form.locator("[name=path]").fill("/ready");
  await form.locator("[name=port]").fill("70000");
  await form.locator("button[type=submit]").click();
  assert.equal(
    requests.checks,
    0,
    "invalid port must be blocked by form validation",
  );
  await form.locator("[name=port]").fill("80");
  await screenshot("linode-check");
  await form.locator("button[type=submit]").click();
  await form.locator("[data-ip-check-submitted]").waitFor();
  assert.equal(requests.checks, 1);

  const change = pane.locator("#ip-change-form");
  await change.locator('[name=method][value="linode-method"]').check();
  await change.locator("[name=available]").selectOption("0");
  await change.locator("[name=postCheck]").check();
  assert.match(
    await change.locator("[data-ip-risk]").innerText(),
    /可能额外收费/,
  );
  await screenshot("linode-change");
  await change.locator("button[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  assert.equal(
    requests.confirm,
    1,
    "IP change must use one confirmation request",
  );
  assert.equal(requests.jobs, 1, "IP change must submit one job");
  assert.match(await page.locator("#content").innerText(), /再次分配了相同 IP/);
  assert.match(
    await page.locator("#content").innerText(),
    /原始公网 IP 未变化/,
  );

  await page.locator("[data-nav=instances]").click();
  await selectAccount("AWS · aws-fixture");
  await page.locator("[data-ip-management]").first().click();
  await pane.locator(".ip-addresses").waitFor();
  assert.match(await pane.innerText(), /可能停机或中断业务/);
  await page.locator("#dialog [data-close]").first().click();
  await selectAccount("Azure · azure-fixture");
  await page.locator("[data-ip-management]").first().click();
  await pane.locator(".ip-addresses").waitFor();
  assert.match(await pane.innerText(), /原机更换方式/);
  await page.locator("#dialog [data-close]").first().click();

  await page.locator("[data-nav=connectivity]").click();
  await page.locator("[data-screening-id=manual-check-id]").waitFor();
  assert.match(await page.locator("#content").innerText(), /手动检测/);
  assert.match(await page.locator("#content").innerText(), /换 IP 后检测/);
  assert.equal(await page.locator("[data-screening-stop]").count(), 0);
  await page.locator("[data-probe-delete=cn-probe]").click();
  await page.getByText("探针已删除", { exact: true }).waitFor();
  await screenshot("connectivity");
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      httpGoFixture: true,
      desktopMobile: true,
      fourProviders: true,
      realCloudWrites: 0,
    }),
  );
} catch (e) {
  await page.screenshot({
    path: `${dir}/ip-management-failure.png`,
    fullPage: true,
  });
  throw e;
} finally {
  await browser.close();
}
