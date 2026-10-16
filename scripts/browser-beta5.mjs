import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";
const url = process.argv[2],
  live = process.env.NARBE_LIVE_NETWORK_TEST === "1";
const browser = await chromium.launch({
  headless: true,
  executablePath:
    process.env.NARBE_BROWSER_EXECUTABLE ||
    (process.platform === "darwin"
      ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
      : undefined),
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1080 } }),
  errors = [],
  jobs = [];
page.on("pageerror", (e) => errors.push(e.message));
page.on("request", (r) => {
  if (r.url().endsWith("/api/jobs") && r.method() === "POST")
    jobs.push(r.postDataJSON());
});
if (!live)
  await page.route("https://api64.ipify.org/**", (r) =>
    r.fulfill({
      status: 200,
      headers: { "Access-Control-Allow-Origin": "*" },
      json: { ip: "198.51.100.42" },
    }),
  );
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
async function shot(name) {
  await page.screenshot({ path: `${dir}/beta5-${name}-desktop.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: `${dir}/beta5-${name}-mobile.png` });
  await page.setViewportSize({ width: 1440, height: 1080 });
}
const settings = async () => {
  await page.locator("[data-nav=settings]").click();
  await page.locator("#network-form").waitFor();
};
try {
  await page.goto(url);
  await page
    .locator("#login [name=password]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator("#login button[type=submit]").click();
  await page.locator("[data-nav=settings]").waitFor();
  await settings();
  let f = page.locator("#network-form");
  assert.equal(await f.locator("[name=mode]").inputValue(), "direct");
  assert.ok(await page.locator("[data-proxy-fields]").isHidden());
  await page.locator("[data-local-test]").click();
  await page.locator("[data-direct-test]").click();
  await page.waitForFunction(
    () =>
      !document
        .querySelector("[data-local-result]")
        .textContent.includes("检测中") &&
      !document
        .querySelector("[data-direct-result]")
        .textContent.includes("检测中"),
  );
  await shot(live ? "actual-egress" : "egress-fixtures");
  if (live) {
    await f.locator("[name=mode]").selectOption("proxy");
    await f.locator("[name=protocol]").selectOption("http");
    await f.locator("[name=host]").fill(process.env.NARBE_LIVE_PROXY_HOST);
    await f.locator("[name=port]").fill(process.env.NARBE_LIVE_PROXY_PORT);
    await f.locator("[name=passwordAction]").selectOption("clear");
    await page.locator("[data-proxy-test]").click();
    await page.waitForFunction(
      () =>
        !document
          .querySelector("[data-proxy-result]")
          ?.textContent.includes("检测中"),
    );
    await shot("actual-egress-three");
    assert.match(
      await page.locator("[data-proxy-result]").innerText(),
      /success/,
    );
    assert.match(
      await page.locator("[data-direct-result]").innerText(),
      /success/,
    );
    console.log(
      JSON.stringify({
        result: "READ_ONLY_LIVE_PROBE",
        browser: await page.locator("[data-local-result]").innerText(),
        server: await page.locator("[data-direct-result]").innerText(),
        proxy: await page.locator("[data-proxy-result]").innerText(),
      }),
    );
  } else {
    assert.match(
      await page.locator("[data-local-result]").innerText(),
      /198\.51\.100\.42/,
    );
    assert.match(
      await page.locator("[data-direct-result]").innerText(),
      /203\.0\.113\.55/,
    );
    await f.locator("[name=mode]").selectOption("proxy");
    await f.locator("[name=protocol]").selectOption("https");
    await f.locator("[name=host]").fill("proxy.example.com");
    await f.locator("[name=port]").fill("8443");
    await f.locator("[name=username]").fill("fixture-user");
    await f.locator("[name=passwordAction]").selectOption("replace");
    await f.locator("[name=password]").fill("private-proxy-password-95!");
    await page.locator("[data-proxy-test]").click();
    await page.waitForFunction(() =>
      document
        .querySelector("[data-proxy-result]")
        .textContent.includes("203.0.113.55"),
    );
    const saved = page.waitForResponse(
      (r) => r.url().endsWith("/api/network") && r.request().method() === "PUT",
    );
    await f.locator("button[type=submit]").click();
    const response = await saved;
    assert.equal(response.status(), 200);
    assert.ok(!(await response.text()).includes("private-proxy"));
    assert.equal(await f.locator("[name=password]").inputValue(), "");
    await shot("proxy-saved");
    await page.goto(url);
    await settings();
    f = page.locator("#network-form");
    assert.equal(await f.locator("[name=mode]").inputValue(), "proxy");
    assert.equal(await f.locator("[name=password]").inputValue(), "");
    await page.getByText("已保存加密密码", { exact: false }).waitFor();
    await f.locator("[name=host]").fill("other.example.com");
    await f.locator("button[type=submit]").click();
    await page
      .getByText("代理身份变化时必须重新输入或明确清除密码", { exact: false })
      .waitFor();
    await f.locator("[name=mode]").selectOption("direct");
    const directSaved = page.waitForResponse(
      (r) => r.url().endsWith("/api/network") && r.request().method() === "PUT",
    );
    await f.locator("button[type=submit]").click();
    assert.equal((await directSaved).status(), 200);
    for (const mode of ["none", "shell", "cloud-config", "stackscript"]) {
      await page.goto(url);
      await page.locator("[data-details]").first().click();
      await page.locator("[data-advanced=rebuild]").click();
      const form = page.locator(".advanced-form");
      await form
        .locator("[name=newImage] option[value=im]")
        .waitFor({ state: "attached" });
      await form.locator("[name=newImage]").selectOption("im");
      await form.locator("[name=password]").fill("Fixture-Strong-Password-95!");
      assert.match(await form.innerText(), /重装会清除所有现有磁盘/);
      assert.match(await form.innerText(), /Metadata：已存在/);
      await form.locator("[name=scriptMode]").selectOption(mode);
      if (mode === "shell" || mode === "cloud-config") {
        await form
          .locator("[name=userData]")
          .fill(
            mode === "shell"
              ? '#!/bin/bash\necho "中文 $HOME"\n'
              : '#cloud-config\nruncmd:\n - echo "中文"\n',
          );
        await form.locator("[name=newImage]").selectOption("im");
        assert.ok(
          (await form.locator("[name=userData]").inputValue()).includes("中文"),
        );
      }
      if (mode === "stackscript") {
        await form
          .locator('[name=stackID] option[value="7"]')
          .waitFor({ state: "attached" });
        await form.locator("[name=stackID]").selectOption("7");
        await form.locator("[data-udf]").fill("private-udf-fixture");
      }
      await form.locator("[name=metadataAcknowledged]").check();
      await form.locator("[name=acknowledge]").check();
      await form.locator("[name=confirmName]").fill("source");
      await shot("rebuild-" + mode);
      const reply = page.waitForResponse(
        (r) => r.url().endsWith("/api/jobs") && r.request().method() === "POST",
      );
      await form.locator("button[type=submit]").click();
      const res = await reply;
      assert.equal(res.status(), 202, await res.text());
      assert.equal(jobs.at(-1).advanced.startup.scriptMode, mode);
      assert.equal(jobs.at(-1).networkRevision, 2);
      await page
        .getByRole("heading", { name: "操作任务", exact: true })
        .waitFor();
      const cancelled = page.waitForResponse(
        (r) => /\/cancel$/.test(r.url()) && r.request().method() === "POST",
      );
      await page.locator("[data-cancel]").first().click();
      assert.equal((await cancelled).status(), 200);
      assert.ok(
        !(await page.locator("#content").innerText()).includes(
          "private-udf-fixture",
        ),
      );
    }
  }
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      jobs: jobs.length,
      realCloudWrites: 0,
      liveIPDetection: live,
      proxyPasswordNotEchoed: true,
    }),
  );
} catch (e) {
  await shot("failure");
  throw e;
} finally {
  await browser.close();
}
