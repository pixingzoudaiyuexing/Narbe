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
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
let provider = "linode",
  bulk = 0,
  catalogFail = false,
  partial = false,
  fail = false,
  delayRegion = "",
  delayedCatalog,
  releaseCatalog,
  delayedInventory,
  releaseInventory,
  delayInventory = false;
const configs = {
  aws: [
    ["ap-northeast-1", "日本东京 · ap-northeast-1"],
    ["us-east-1", "美国东部（弗吉尼亚北部） · us-east-1"],
  ],
  azure: [
    ["japaneast", "日本东京／埼玉 · japaneast"],
    ["eastus", "美国弗吉尼亚 · eastus"],
  ],
  linode: [
    ["ap-northeast", "日本东京 2 · ap-northeast"],
    ["gb-lon", "英国伦敦 2 · gb-lon"],
  ],
  digitalocean: [
    ["sgp1", "新加坡 · sgp1"],
    ["nyc3", "美国纽约 3 · nyc3"],
  ],
};
function regions() {
  return configs[provider].map(([id, name]) => ({ id, name }));
}
function pricing() {
  return ["linode", "digitalocean"].includes(provider)
    ? { monthly: 12, hourly: 0.02, currency: "USD", source: "官方API测试替身" }
    : undefined;
}
function items() {
  const base = regions().map((r, n) => ({
    id: "same-id",
    name: "same-server",
    region: r.id,
    regionName: r.name,
    state: n ? "offline" : "running",
    plan:
      provider === "aws"
        ? "t3.medium"
        : provider === "azure"
          ? "Standard_B2s"
          : "small",
    cpu: 2,
    memory: 4096,
    disk: ["aws", "azure"].includes(provider) ? 0 : 50,
    architecture: "x86_64",
    pricing: pricing(),
    ips: [n ? "8.8.4.4" : "8.8.8.8", "2001:4860::1"],
    image: "Debian 13",
    actions: n ? ["start", "delete"] : ["stop", "reboot", "delete"],
  }));
  return bulk
    ? Array.from({ length: bulk }, (_, n) => ({
        ...base[0],
        id: "bulk-" + n,
        name: "bulk-server-" + n,
      }))
    : base;
}
await page.route("**/api/**", async (route) => {
  const req = route.request();
  assert.equal(
    req.method(),
    "GET",
    "browser inspection must never submit cloud operations",
  );
  const u = new URL(req.url()),
    path = u.pathname.slice(5),
    aid = u.searchParams.get("accountId");
  let body;
  if (path === "session") body = { csrf: "fixture-csrf" };
  else if (path === "accounts")
    body = [
      {
        id: "fixture",
        name: "四云测试账号",
        provider,
        created: 1,
        revision: 1,
      },
      { id: "other", name: "空账号", provider, created: 2, revision: 1 },
    ];
  else if (path === "jobs") body = [];
  else if (path === "linode")
    body = {
      accountId: aid,
      accountRevision: 1,
      data:
        new URL(req.url()).searchParams.get("view") === "instance"
          ? {
              instance: items()[0],
              snapshot: "fixture",
              generation: "legacy_config",
              disks: [],
              configs: [],
              interfaces: [],
              volumes: [],
            }
          : { items: [], snapshot: "fixture" },
    };
  else if (path === "inventory") {
    if (aid === "fixture" && delayInventory) {
      delayInventory = false;
      delayedInventory();
      await new Promise((yes) => {
        releaseInventory = yes;
      });
    }
    if (fail && aid === "fixture")
      return route.fulfill({ status: 502, json: { error: "测试查询失败" } });
    if (u.searchParams.has("retry")) partial = false;
    const all = aid === "other" ? [] : items();
    const result = partial ? all.slice(0, 1) : all;
    body = {
      accountId: aid,
      accountRevision: 1,
      phase: "ready",
      complete: !partial,
      items: u.searchParams.has("summary") ? [] : result,
      regions: regions(),
      failures: partial
        ? [{ region: regions()[1].id, message: "权限不足" }]
        : [],
      succeeded: partial ? [regions()[0].id] : regions().map((r) => r.id),
      warnings: [],
      scope:
        provider === "azure"
          ? "当前接入资源组 · 全部地区（非整个订阅）"
          : "当前账号 · 全部地区",
      stats: {
        total: result.length,
        running: result.filter((i) => i.state === "running").length,
        stopped: result.filter((i) => i.state === "offline").length,
        other: 0,
      },
      startedAt: Date.UTC(2026, 9, 14, 9, 59) / 1000,
      updatedAt: Date.UTC(2026, 9, 14, 10, 0) / 1000,
      nextRefreshAt: 0,
    };
  } else if (path === "catalog") {
    const r = u.searchParams.get("region");
    if (catalogFail && r)
      return route.fulfill({
        status: 503,
        json: { error: "配置查询暂不可用" },
      });
    if (r && r === delayRegion) {
      delayRegion = "";
      delayedCatalog();
      await new Promise((yes) => {
        releaseCatalog = yes;
      });
    }
    const arm = r === regions()[1].id;
    body = {
      regions: regions(),
      plans: r
        ? [
            {
              id: arm ? "arm-plan" : "p",
              name: "标准规格",
              cpu: 2,
              memory: 4096,
              disk: ["linode", "digitalocean"].includes(provider) ? 50 : 0,
              architecture: arm ? "arm64" : "x86_64",
              pricing: pricing(),
            },
          ]
        : [],
      images: r
        ? [
            {
              id: "im",
              name: "Debian 13",
              architecture: "x86_64",
              family: "debian",
              disk: 10,
            },
            {
              id: "arm-im",
              name: "Debian ARM64",
              architecture: "arm64",
              family: "debian",
              disk: 10,
            },
            {
              id: "large-im",
              name: "Too large",
              architecture: "x86_64",
              family: "debian",
              disk: 1000,
            },
          ]
        : [],
      networks: [{ id: "net", name: "已有网络" }],
      groups: [{ id: "group", name: "已有安全组" }],
      keys: [{ id: "key", name: "已有SSH密钥" }],
    };
  } else throw Error("Unexpected API route: " + path);
  await route.fulfill({ status: 200, json: body });
});
try {
  for (const name of ["aws", "azure", "linode", "digitalocean"]) {
    provider = name;
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(url.toString());
    assert.deepEqual(
      await page.evaluate(() => ({
        secure: isSecureContext,
        uuid: typeof crypto.randomUUID,
        random: typeof crypto.getRandomValues,
      })),
      { secure: false, uuid: "undefined", random: "function" },
    );
    await page.locator("td .name").nth(1).waitFor();
    assert.equal(await page.locator(".count").innerText(), "2");
    if (provider === "azure")
      await page
        .getByText("当前接入资源组 · 全部地区（非整个订阅）", { exact: false })
        .waitFor();
    await page.screenshot({
      path: `${dir}/${provider}-servers.png`,
      fullPage: true,
    });
    await page
      .locator("#region-search")
      .fill(regions()[0].name.split(" · ")[0]);
    assert.equal(
      await page.locator("#region option").count(),
      2,
      "Chinese region search should leave placeholder and matching region",
    );
    await page.locator("#region-search").fill("");
    await page.locator("#region").selectOption(regions()[0].id);
    assert.equal(await page.locator("td .name").count(), 1);
    await page.locator("#region").selectOption("");
    await page.locator("#status-filter").selectOption("stopped");
    assert.equal(await page.locator("td .name").count(), 1);
    await page.locator("#status-filter").selectOption("");
    await page.locator("#instance-search").fill("8.8.4.4");
    assert.equal(await page.locator("td .name").count(), 1);
    await page.locator('[data-action="0"]').click();
    await page.locator("dialog input[name=confirmName]").waitFor();
    assert.ok(
      (await page.locator("dialog").innerText()).includes(regions()[1].name),
      "filtered row action changed region identity",
    );
    assert.ok(
      (await page.locator("dialog").innerText()).includes("四云测试账号"),
    );
    await page.locator("dialog header [data-close]").click();
    await page.locator("#instance-search").fill("");
    await page.locator('[data-details="0"]').click();
    await page.getByRole("heading", { name: "服务器详情" }).waitFor();
    await page
      .locator("dialog")
      .getByText("2 vCPU", { exact: false })
      .waitFor();
    await page.locator("dialog header [data-close]").click();
    await page.locator("#create").click();
    await page.locator("#create-form").waitFor();
    const f = page.locator("#create-form");
    await f.locator("[name=name]").fill("test-server");
    await f.locator("[name=region]").selectOption(regions()[0].id);
    await f
      .locator("[name=plan] option[value=p]")
      .waitFor({ state: "attached" });
    if (provider === "aws") {
      catalogFail = true;
      await f.locator("#retry-catalog").click();
      await f
        .locator(".error-box")
        .getByText("配置查询暂不可用", { exact: true })
        .waitFor();
      catalogFail = false;
      await f.locator("#retry-catalog").click();
      await f
        .locator("[name=plan] option[value=p]")
        .waitFor({ state: "attached" });
    }
    await f.locator("[name=plan]").selectOption("p");
    await f.locator("[name=image]").selectOption("im");
    if (["linode", "digitalocean"].includes(provider))
      assert.equal(
        await f.locator("[name=image] option[value=large-im]").count(),
        0,
        "disk incompatible image shown",
      );
    assert.equal(
      await f.locator("[name=image] option[value=arm-im]").count(),
      0,
      "architecture incompatible image shown",
    );
    await f.locator("[name=confirmName]").fill("test-server");
    if (["aws", "azure"].includes(provider))
      await f.locator("[name=network]").selectOption("net");
    if (provider === "aws")
      await f.locator("[name=group]").selectOption("group");
    if (provider !== "azure") await f.locator("[name=key]").selectOption("key");
    assert.ok(
      (await f.locator("#create-summary").innerText()).includes(
        "2 vCPU / 4 GB 内存",
      ),
    );
    assert.ok(
      (await f.locator("#create-summary").innerText()).includes(
        regions()[0].name,
      ),
    );
    if (pricing())
      assert.ok(
        (await f.locator("#plan-info").innerText()).includes("12 USD/月"),
      );
    else
      assert.ok(
        (await f.locator("#plan-info").innerText()).includes(
          "无法获取准确报价",
        ),
      );
    await page.screenshot({
      path: `${dir}/${provider}-create.png`,
      fullPage: true,
    });
    await f.locator("[name=name]").scrollIntoViewIfNeeded();
    await page.screenshot({
      path: `${dir}/${provider}-create-top.png`,
      fullPage: true,
    });
    const waiting = new Promise((yes) => {
      delayedCatalog = yes;
    });
    delayRegion = regions()[1].id;
    await f.locator("[name=region]").selectOption(regions()[1].id);
    await waiting;
    await f.locator("[name=region]").selectOption(regions()[0].id);
    await f
      .locator("[name=plan] option[value=p]")
      .waitFor({ state: "attached" });
    releaseCatalog();
    assert.equal(
      await f.locator("[name=plan] option[value=arm-plan]").count(),
      0,
      "old region response overwrote new options",
    );
    assert.equal(
      await f.locator("[name=image]").inputValue(),
      "",
      "region change retained stale image",
    );
    await page.locator("dialog header [data-close]").click();
    await page.getByRole("button", { name: "云账号", exact: true }).click();
    await page.getByText("共 2 台服务器", { exact: true }).waitFor();
    await page.screenshot({
      path: `${dir}/${provider}-accounts.png`,
      fullPage: true,
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({
      path: `${dir}/${provider}-accounts-mobile.png`,
      fullPage: true,
    });
    await page.getByRole("button", { name: "服务器", exact: true }).click();
    await page.locator("td .name").nth(1).waitFor();
    await page.screenshot({
      path: `${dir}/${provider}-servers-mobile.png`,
      fullPage: true,
    });
  }
  partial = true;
  await page.locator("#refresh").click();
  await page
    .getByText(
      "已发现 1 台服务器，另有 1 个地区或查询范围失败，总数暂未确认。",
      { exact: true },
    )
    .waitFor();
  assert.equal(await page.locator("td .name").count(), 1);
  await page.getByRole("button", { name: "重试此范围" }).click();
  await page.locator("td .name").nth(1).waitFor();
  fail = true;
  await page.locator("#refresh").click();
  await page
    .locator("#content > .error-box")
    .getByText("测试查询失败", { exact: true })
    .waitFor();
  assert.ok(
    (await page.locator(".inventory-summary").innerText()).includes(
      "当前总数未确认",
    ),
  );
  assert.ok(
    (await page.locator(".inventory-summary").innerText()).includes("上次查询"),
    "transport failure must label retained rows as old data",
  );
  fail = false;
  await page.locator("#refresh").click();
  await page.locator("td .name").nth(1).waitFor();
  const waiting = new Promise((yes) => {
    delayedInventory = yes;
  });
  delayInventory = true;
  await page.locator("#refresh").click();
  await waiting;
  await page.locator("#account").selectOption("other");
  await page.getByRole("heading", { name: "当前范围还没有服务器" }).waitFor();
  releaseInventory();
  await page.getByRole("heading", { name: "当前范围还没有服务器" }).waitFor();
  assert.equal(
    await page.locator("td .name").count(),
    0,
    "old account request populated another account",
  );
  assert.equal(await page.locator("#account").inputValue(), "other");
  const reentering = new Promise((yes) => {
    delayedInventory = yes;
  });
  delayInventory = true;
  await page.locator("#account").selectOption("fixture");
  await reentering;
  assert.equal(
    await page.locator("td .name").count(),
    0,
    "previously selected account details must be discarded rather than retained without bound",
  );
  releaseInventory();
  await page.locator("td .name").nth(1).waitFor();
  bulk = 123;
  await page.reload();
  await page.getByText("共 123 台服务器", { exact: true }).waitFor();
  assert.equal(
    await page.locator("td .name").count(),
    50,
    "large inventory should render only one page",
  );
  await page.locator("#next-page").click();
  assert.equal(await page.locator("td .name").count(), 50);
  await page.locator('[data-action="0"]').click();
  await page
    .locator("dialog dl")
    .getByText("bulk-50", { exact: true })
    .waitFor();
  await page.locator("dialog header [data-close]").click();
  await page.locator("#next-page").click();
  assert.equal(await page.locator("td .name").count(), 23);
  await page.locator("#instance-search").fill("bulk-server-122");
  assert.equal(
    await page.locator("td .name").count(),
    1,
    "search must cover rows outside the current page",
  );
  await page.addInitScript(() =>
    Object.defineProperty(Crypto.prototype, "getRandomValues", {
      value: undefined,
    }),
  );
  await page.reload();
  await page.locator("td .name").first().waitFor();
  await page.locator("#create").click();
  await page
    .getByText("浏览器功能不兼容，暂时无法提交操作。请升级浏览器后重试", {
      exact: true,
    })
    .waitFor();
  assert.equal(
    await page.locator("#create-form").count(),
    0,
    "missing CSPRNG must fail closed",
  );
  assert.deepEqual(errors, [], "page errors");
  console.log(
    "PASS: four-cloud real HTTP browser UI (GET-only API fixtures): account counts, all regions, Chinese search, state/name/IP filters, configuration/price/disk summaries, compatible images, action identity, partial failure/retry, region/account races, desktop/mobile screenshots; no cloud writes.",
  );
} finally {
  await browser.close();
}
