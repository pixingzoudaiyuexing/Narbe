import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";

const url = new URL(process.argv[2]);
url.hostname = "narbe.test";
const browser = await chromium.launch({
  executablePath:
    process.env.NARBE_BROWSER_EXECUTABLE ||
    (process.platform === "darwin"
      ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
      : undefined),
  headless: true,
  args: ["--host-resolver-rules=MAP narbe.test 127.0.0.1", "--no-proxy-server"],
});
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const errors = [];
page.on("pageerror", (e) => errors.push(e.message));
const requests = [];
let fail = false;
let provider = "linode";
const instances = [
  {
    id: "1",
    name: "london-server",
    region: "gb-lon",
    state: "running",
    plan: "small",
    ips: [],
    actions: ["reboot", "delete"],
  },
  {
    id: "2",
    name: "tokyo-server",
    region: "ap-northeast",
    state: "running",
    plan: "small",
    ips: [],
    actions: ["reboot", "delete"],
  },
];
await page.route("**/api/**", async (route) => {
  const request = route.request();
  assert.equal(
    request.method(),
    "GET",
    "UI inspection must never submit a cloud operation",
  );
  const u = new URL(request.url());
  const path = u.pathname.slice(5);
  let body;
  if (path === "session") body = { csrf: "fixture-csrf" };
  else if (path === "accounts")
    body = [
      { id: "fixture", name: "测试 Linode", provider, created: 1 },
      { id: "other", name: "空账号", provider, created: 2 },
    ];
  else if (path === "jobs") body = [];
  else if (path === "catalog")
    body = {
      regions: [
        { id: "gb-lon", name: "London" },
        { id: "ap-northeast", name: "Tokyo" },
      ],
      plans: [{ id: "small", name: "Small", architecture: "x86_64" }],
      images: [
        {
          id: "debian",
          name: "Debian",
          family: "debian",
          architecture: "x86_64",
        },
      ],
      keys: [],
    };
  else if (path === "instances") {
    const region = u.searchParams.get("region") || "";
    requests.push(region);
    if (fail)
      return route.fulfill({ status: 502, json: { error: "测试查询失败" } });
    body =
      u.searchParams.get("accountId") === "other"
        ? []
        : instances.filter((i) => !region || i.region === region);
  } else throw Error("Unexpected API route: " + path);
  await route.fulfill({ status: 200, json: body });
});
try {
  await page.goto(url.toString());
  assert.deepEqual(
    await page.evaluate(() => ({
      secure: isSecureContext,
      uuid: typeof crypto.randomUUID,
      random: typeof crypto.getRandomValues,
    })),
    { secure: false, uuid: "undefined", random: "function" },
  );
  if (process.env.NARBE_REGRESSION_CASE === "http") {
    await page.locator("#create").click();
    await page.locator("#create-form").waitFor({ timeout: 3000 });
    assert.deepEqual(errors, []);
    console.log(
      "PASS: actual non-loopback HTTP opens create dialog without randomUUID or writes.",
    );
  } else {
    await page
      .getByText("london-server", { exact: true })
      .waitFor({ timeout: 3000 });
    await page.getByText("tokyo-server", { exact: true }).waitFor();
    assert.equal(await page.locator(".count").innerText(), "2");
    assert.ok(requests.includes(""), "initial view must query all regions");
    await page.locator("#region").selectOption("gb-lon");
    await page
      .getByText("tokyo-server", { exact: true })
      .waitFor({ state: "hidden" });
    assert.equal(await page.locator(".count").innerText(), "1");
    await page.locator("#region").selectOption("");
    await page.getByText("tokyo-server", { exact: true }).waitFor();
    await page.locator("#create").click();
    await page.locator("#create-form").waitFor();
    await page
      .locator("#create-form select[name=region]")
      .selectOption("ap-northeast");
    await page
      .locator("#create-form select[name=plan] option[value=small]")
      .waitFor({ state: "attached" });
    await page
      .locator("#create-form select[name=image] option[value=debian]")
      .waitFor({ state: "attached" });
    const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
    fs.mkdirSync(dir, { recursive: true });
    await page.screenshot({ path: dir + "/http-create.png", fullPage: true });
    await page.locator("dialog header [data-close]").click();
    await page.screenshot({ path: dir + "/all-regions.png", fullPage: true });
    await page.locator('[data-action="0"]').click();
    await page.locator("dialog input[name=confirmName]").waitFor();
    await page.locator("dialog header [data-close]").click();
    fail = true;
    await page.locator("#refresh").click();
    await page.getByText("测试查询失败", { exact: true }).waitFor();
    assert.equal(
      await page.locator("td .name").count(),
      0,
      "failure must not present stale data as a current total",
    );
    fail = false;
    await page.locator("#refresh").click();
    await page.getByText("tokyo-server", { exact: true }).waitFor();
    await page.locator("#account").selectOption("other");
    await page.getByRole("heading", { name: "当前范围还没有服务器" }).waitFor();
    assert.equal(await page.locator(".count").innerText(), "0");
    await page.locator("#account").selectOption("fixture");
    await page.getByText("tokyo-server", { exact: true }).waitFor();
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({
      path: dir + "/all-regions-mobile.png",
      fullPage: true,
    });
    for (const name of ["digitalocean", "azure", "aws"]) {
      provider = name;
      requests.length = 0;
      await page.reload();
      await page
        .locator("#region option[value=gb-lon]")
        .waitFor({ state: "attached" });
      if (name === "aws") {
        await page.getByRole("heading", { name: "选择一个地区" }).waitFor();
        assert.deepEqual(
          requests,
          [],
          "AWS must not issue an unsupported all-region query",
        );
        assert.equal(await page.locator(".count").innerText(), "—");
      } else {
        await page.getByText("tokyo-server", { exact: true }).waitFor();
        assert.equal(await page.locator(".count").innerText(), "2");
        if (name === "azure")
          await page
            .getByText("Azure · 当前资源组 · 全部地区", { exact: true })
            .waitFor();
      }
      await page.locator("#region").selectOption("gb-lon");
      await page.getByText("london-server", { exact: true }).waitFor();
      await page
        .getByText("tokyo-server", { exact: true })
        .waitFor({ state: "hidden" });
    }
    assert.deepEqual(errors, []);
    console.log(
      "PASS: HTTP all-region total/filter/restore, create catalog and action dialogs, error/retry, desktop/mobile; all API requests GET only.",
    );
  }
} finally {
  await browser.close();
}
