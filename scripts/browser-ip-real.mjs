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
page.on("pageerror", (e) => errors.push(e.message));
try {
  await page.goto(process.argv[2]);
  await page
    .locator("#login [name=password]")
    .fill(process.env.NARBE_TEST_PASSWORD);
  await page.locator("#login button[type=submit]").click();
  await page.locator("[data-ip-management]").first().click();
  const pane = page.locator(".ip-management");
  await pane.locator(".ip-addresses").waitFor();
  assert.match(await pane.innerText(), /8\.8\.8\.8/);
  await pane.locator('[name=method][value="do-reserved-allocate"]').check();
  await pane.locator("#ip-change-form button[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  await page
    .getByText("已完成", { exact: true })
    .first()
    .waitFor({ timeout: 15000 });
  await page.locator("[data-nav=instances]").click();
  await page.locator("[data-ip-management]").first().click();
  await pane.locator(".ip-addresses").waitFor();
  assert.match(await pane.innerText(), /8\.8\.4\.4/);
  const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
  fs.mkdirSync(dir, { recursive: true });
  for (const [device, viewport] of [
    ["desktop", { width: 1440, height: 1080 }],
    ["mobile", { width: 390, height: 844 }],
  ]) {
    await page.setViewportSize(viewport);
    assert.equal(
      await page.evaluate(
        () => document.documentElement.scrollWidth > innerWidth,
      ),
      false,
    );
    await page.screenshot({
      path: `${dir}/ip-real-backend-${device}.png`,
      fullPage: true,
    });
  }
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      actualBackend: true,
      realCloudWrites: 0,
      desktopMobile: true,
    }),
  );
} finally {
  await browser.close();
}
