import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";

// All API reads and writes are fixture responses. Provider network access is
// blocked, and only confirm/jobs POSTs may reach the fixture dispatcher.
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
const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
const dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
const errors = [],
  requests = [],
  confirms = [],
  jobs = [];
page.on("pageerror", (e) => errors.push(e.message));
let provider = "linode",
  generation = "legacy_config",
  instanceState = "offline";
let jobResponses = [],
  instanceOverrides = {};
let billingDenied = false,
  unknownBilling = false,
  emptyStats = false;
let failView = "",
  emptyView = "",
  delayedView = "",
  delayedPeriod = "";
let delayedStarted, releaseDelayed;
let catalogDenied = false;
let catalogDelayRegion = "",
  catalogDelayStarted,
  releaseCatalog;
const regionsByProvider = {
  aws: [
    ["ap-northeast-1", "日本东京", "亚洲"],
    ["us-east-1", "美国弗吉尼亚北部", "北美洲"],
    ["ap-southeast-2", "澳大利亚悉尼", "大洋洲"],
    ["ap-future-fixture", "位置未确认", "其他／位置待确认"],
  ],
  azure: [
    ["japaneast", "日本东京／埼玉", "亚洲"],
    ["eastus", "美国弗吉尼亚", "北美洲"],
    ["australiaeast", "澳大利亚新南威尔士", "大洋洲"],
    ["azure-future-fixture", "位置未确认", "其他／位置待确认"],
  ],
  linode: [
    ["ap-northeast", "日本东京 2", "亚洲"],
    ["gb-lon", "英国伦敦 2", "欧洲"],
    ["ap-southeast", "澳大利亚悉尼", "大洋洲"],
    ["linode-future-fixture", "位置未确认", "其他／位置待确认"],
  ],
  digitalocean: [
    ["sgp1", "新加坡", "亚洲"],
    ["nyc3", "美国纽约 3", "北美洲"],
    ["syd1", "澳大利亚悉尼", "大洋洲"],
    ["do-future-fixture", "位置未确认", "其他／位置待确认"],
  ],
};
const regions = () =>
  regionsByProvider[provider].map(([id, name, continent]) => ({
    id,
    name: `${name} · ${id}`,
    continent,
  }));
const pricing = {
  monthly: 12,
  hourly: 0.018,
  currency: "USD",
  source: "Linode官方API合同测试替身",
};
function instance() {
  return {
    id: "42",
    name: "fixture-source",
    region: regions()[0].id,
    regionName: regions()[0].name,
    state: instanceState,
    plan: "g6-standard-1",
    image: "linode/debian13",
    cpu: 1,
    memory: 2048,
    disk: 50,
    diskKind: "套餐磁盘",
    architecture: "x86_64",
    pricing,
    ips: ["8.8.8.8", "2001:4860::42"],
    actions: ["start", "delete"],
    ...instanceOverrides,
  };
}
const publicDevice = (id, label) => ({
  id,
  entity: { id, type: "linode", label, url: `/v4/linode/instances/${id}` },
});
const baseRules = () => ({
  inbound_policy: "DROP",
  outbound_policy: "ACCEPT",
  outbound: [],
  inbound: [
    {
      action: "ACCEPT",
      protocol: "TCP",
      ports: "22,443",
      addresses: { ipv4: ["0.0.0.0/0"], ipv6: ["::/0"] },
    },
  ],
});
function firewallList() {
  return {
    items: [
      {
        id: 7,
        label: "shared-fixture",
        status: "enabled",
        rules: baseRules(),
        devices: [
          publicDevice(42, "fixture-source"),
          publicDevice(84, "fixture-neighbor"),
        ],
        snapshot: "firewall-7-snapshot",
      },
      {
        id: 8,
        label: "unused-fixture",
        status: "disabled",
        rules: {
          inbound_policy: "ACCEPT",
          outbound_policy: "ACCEPT",
          inbound: [],
          outbound: [],
        },
        devices: [],
        snapshot: "firewall-8-snapshot",
      },
    ],
    snapshot: "firewall-list-snapshot",
    queriedAt: 1780000000,
  };
}
function stats(period) {
  const points = emptyStats
    ? []
    : [
        [1760000000000, 25],
        [1760000300000, 70],
        [1760000600000, 40],
      ];
  return {
    source: "Linode Stats API",
    queriedAt: 1780000000,
    period: period
      ? period === new Date().toISOString().slice(0, 7)
        ? "过去30天（当前年月接口）"
        : period
      : "过去24小时",
    title: "fixture official statistics",
    series: [
      {
        key: "cpu",
        label: "CPU使用率",
        unit: "%",
        points,
        sampleSeconds: points.length ? 300 : undefined,
      },
      {
        key: "io.io",
        label: "磁盘 I/O",
        unit: "Block/s",
        points,
        sampleSeconds: points.length ? 300 : undefined,
      },
      {
        key: "netv4.in",
        label: "IPv4 入站",
        unit: "bits/s",
        points,
        sampleSeconds: points.length ? 300 : undefined,
      },
      {
        key: "netv6.out",
        label: "IPv6 出站",
        unit: "bits/s",
        points,
        sampleSeconds: points.length ? 300 : undefined,
      },
    ],
  };
}
function inventory(aid, summary) {
  const i = instance();
  return {
    accountId: aid,
    accountRevision: 3,
    phase: "ready",
    complete: true,
    items: summary ? [] : [i],
    regions: regions(),
    failures: [],
    warnings: [],
    succeeded: regions().map((r) => r.id),
    scope:
      provider === "azure"
        ? "当前接入资源组 · 全部地区（非整个订阅）"
        : "当前账号 · 全部地区",
    stats: {
      total: 1,
      running: instanceState === "running" ? 1 : 0,
      stopped: instanceState === "offline" ? 1 : 0,
      other: 0,
    },
    startedAt: 1780000000,
    updatedAt: 1780000001,
    nextRefreshAt: 0,
  };
}
await page.route("**/*", async (route) => {
  const req = route.request(),
    u = new URL(req.url());
  if (u.origin !== url.origin) {
    errors.push(
      `Unexpected external network request blocked: ${req.method()} ${u.origin}`,
    );
    await route.abort();
    return;
  }
  if (!u.pathname.startsWith("/api/")) {
    assert.equal(req.method(), "GET");
    await route.continue();
    return;
  }
  const path = u.pathname.slice(5),
    aid = u.searchParams.get("accountId");
  requests.push({
    path,
    view: u.searchParams.get("view"),
    period: u.searchParams.get("period"),
    method: req.method(),
  });
  try {
    if (req.method() !== "GET") {
      assert.equal(
        req.method(),
        "POST",
        "Only mocked confirm/jobs POSTs are permitted",
      );
      assert.ok(
        ["confirm", "jobs"].includes(path),
        `Unexpected mutation ${path}`,
      );
      assert.equal(req.headers()["x-csrf-token"], "fixture-csrf");
      const body = req.postDataJSON();
      assert.equal(body.accountId, "fixture");
      assert.match(
        body.id,
        /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
      );
      if (path === "confirm") {
        confirms.push(structuredClone(body));
        await route.fulfill({
          json: {
            confirmation: `fixture-confirmation-${body.id}`,
            accountRevision: 3,
          },
        });
      } else {
        const confirmation = confirms.findLast((c) => c.id === body.id);
        assert.ok(confirmation, "Job bypassed mocked confirmation");
        assert.equal(body.confirmation, `fixture-confirmation-${body.id}`);
        assert.equal(body.accountRevision, 3);
        assert.deepEqual(body.advanced, confirmation.advanced);
        assert.deepEqual(body.spec, confirmation.spec);
        jobs.push(structuredClone(body));
        await route.fulfill({
          status: 202,
          json: { id: body.id, state: "queued" },
        });
      }
      return;
    }
    let body;
    if (path === "session") body = { csrf: "fixture-csrf" };
    else if (path === "accounts")
      body = [
        {
          id: "fixture",
          name: "高级管理测试账号",
          provider,
          revision: 3,
          created: 1780000000,
        },
      ];
    else if (path === "jobs") body = jobResponses;
    else if (path === "inventory")
      body = inventory(aid, u.searchParams.has("summary"));
    else if (path === "catalog") {
      if (catalogDenied)
        return await route.fulfill({
          status: 503,
          json: { error: "测试目录暂不可用，请重试" },
        });
      const region = u.searchParams.get("region");
      if (region && region === catalogDelayRegion) {
        catalogDelayRegion = "";
        catalogDelayStarted?.();
        await new Promise((yes) => {
          releaseCatalog = yes;
        });
      }
      body = {
        regions: regions(),
        plans: region
          ? [
              {
                id: "g6-standard-1",
                name: "Linode 2GB",
                cpu: 1,
                memory: 2048,
                disk: 50,
                architecture: "x86_64",
                pricing,
              },
              {
                id: "g6-standard-2",
                name: "Linode 4GB",
                cpu: 2,
                memory: 4096,
                disk: 80,
                architecture: "x86_64",
                pricing: { ...pricing, monthly: region === "gb-lon" ? 25 : 24 },
              },
            ]
          : [],
        images: region
          ? [
              {
                id: "linode/debian13",
                name: "Debian 13",
                architecture: "x86_64",
                disk: 10,
                family: "debian",
              },
            ]
          : [],
        keys: [
          {
            id: "5",
            name: "fixture SSH key",
            extra: {
              publicKey:
                "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureForBrowserOnly",
            },
          },
        ],
        networks: [],
        groups: [],
      };
    } else if (path === "linode") {
      const view = u.searchParams.get("view"),
        period = u.searchParams.get("period"),
        target = u.searchParams.get("target");
      if (
        view === delayedView &&
        (!delayedPeriod || period === delayedPeriod || target === delayedPeriod)
      ) {
        delayedView = "";
        delayedStarted?.();
        await new Promise((yes) => {
          releaseDelayed = yes;
        });
      }
      if (view === failView || (view === "billing" && billingDenied))
        return await route.fulfill({
          status: 403,
          json: { error: "缺少账单或资源读取权限（测试）" },
        });
      if (view === emptyView)
        return await route.fulfill({
          json: { accountId: aid, accountRevision: 3, data: null },
        });
      if (view === "instance")
        body = {
          instance: instance(),
          generation,
          snapshot: "instance-42-snapshot",
          queriedAt: 1780000000,
          disks: [
            {
              id: 100,
              label: "root-disk",
              filesystem: "ext4",
              size: 50000,
              status: "ready",
            },
            {
              id: 101,
              label: "second-system",
              filesystem: "ext4",
              size: 1000,
              status: "ready",
            },
            {
              id: 102,
              label: "swap",
              filesystem: "swap",
              size: 1024,
              status: "ready",
            },
          ],
          configs: [],
          volumes: [],
          interfaces:
            generation === "linode"
              ? [
                  { id: 55, public: {} },
                  { id: 56, vpc: { vpc_id: 10 } },
                  { id: 57, vlan: { vlan: "private" } },
                ]
              : [],
        };
      else if (view === "transfer")
        body = period
          ? {
              source: "Linode Transfer API",
              queriedAt: 1780000000,
              period,
              inBytes: 1 * 2 ** 30,
              outBytes: 2 * 2 ** 30,
              totalBytes: 3 * 2 ** 30,
            }
          : {
              source: "Linode Transfer API",
              queriedAt: 1780000000,
              period: new Date().toISOString().slice(0, 7),
              usedBytes: 2 ** 30,
              quotaGB: 2000,
              billableGB: 0,
              accountPool: {
                usedGB: 5,
                quotaGB: 5000,
                billableGB: 0,
                regions: [{ id: "id-cgk", used: 2, quota: 200, billable: 0 }],
              },
            };
      else if (view === "stats") body = stats(period);
      else if (view === "firewalls") body = firewallList();
      else if (view === "instance-firewalls")
        body = {
          items: firewallList().items.slice(0, 1),
          snapshot: `binding-${u.searchParams.get("interfaceId") || "legacy"}`,
        };
      else if (view === "billing")
        body = {
          balance: unknownBilling ? null : -25,
          uninvoiced: unknownBilling ? null : 12.5,
          currency: "USD",
          source: "Linode Account API",
          queriedAt: 1780000000,
          estimateNote: "未出账费用为估算，不含流量费用，最终以账单为准",
        };
      else if (view === "invoices")
        body = {
          invoices: {
            data: [
              {
                id: 9,
                date: "2026-09-01T00:00:00",
                label: "invoice-nine",
                total: unknownBilling ? null : 13.5,
              },
              {
                id: 10,
                date: "2026-08-01T00:00:00",
                label: "invoice-ten",
                total: 9,
              },
            ],
            page: Number(u.searchParams.get("page") || 1),
            pages: 2,
            results: 27,
          },
          currency: "USD",
          statusNote: "付款状态官方未提供",
        };
      else if (view === "invoice")
        body = {
          invoice: {
            id: Number(target),
            label: target === "9" ? "invoice-nine" : "invoice-ten",
            total: target === "9" ? 13.5 : 9,
          },
          items: {
            data: [
              {
                label: `fixture-item-${target}`,
                quantity: 5,
                unit_price: 2.5,
                amount: 12.5,
                total: 13.5,
                tax: 1,
                from: "2026-08-01",
                to: "2026-08-31",
              },
            ],
            page: 1,
            pages: 1,
          },
          currency: "USD",
          statusNote: "付款状态官方未提供",
        };
      else if (view === "permissions")
        body = [
          {
            name: "服务器",
            status: "可读取",
            scope: "linodes:read_write",
            write: "Token包含写scope；仍需云端校验资源授权",
          },
          {
            name: "账单",
            status: "读取不可用",
            scope: "account:read_only",
            write: "只读",
          },
        ];
      else throw Error(`Unexpected Linode fixture view ${view}`);
      body = { accountId: aid, accountRevision: 3, data: body };
    } else throw Error(`Unexpected API fixture ${path}`);
    await route.fulfill({ status: 200, json: body });
  } catch (e) {
    errors.push(e.message);
    await route.fulfill({ status: 500, json: { error: "测试替身断言失败" } });
  }
});

const dialog = page.locator("#dialog");
const close = async () => {
  if (await dialog.isVisible())
    await dialog.locator("[data-close]").first().click();
};
async function fresh(next = "linode") {
  provider = next;
  await page.goto(url.toString());
  await page.locator("[data-details]").first().waitFor();
}
async function details() {
  await page.locator('[data-nav="instances"]').click();
  await page.locator("[data-details]").first().click();
  await dialog.locator('[data-detail="manage"]').waitFor();
}
async function accountView(view) {
  await close();
  await page.locator('[data-nav="accounts"]').click();
  await page.locator(`[data-${view}]`).first().click();
}
async function advanced(action) {
  await fresh();
  await details();
  await dialog.locator(`[data-advanced="${action}"]`).click();
  await dialog.locator(".advanced-form").waitFor();
}
async function completeAction(action, name = "fixture-source") {
  const form = dialog.locator("form"),
    before = jobs.length;
  await form.locator('[name="acknowledge"]').check();
  await form.locator('[name="confirmName"]').fill(name);
  await form.locator('button[type="submit"]').click();
  await dialog.waitFor({ state: "hidden" });
  assert.equal(
    jobs.length,
    before + 1,
    `No mocked job submitted for ${action}`,
  );
  const job = jobs.at(-1);
  assert.equal(job.action, action);
  assert.equal(job.confirmName, name);
  assert.equal(job.advanced.acknowledge, true);
  return job;
}
function delay(view, period = "") {
  delayedView = view;
  delayedPeriod = period;
  return new Promise((yes) => {
    delayedStarted = yes;
  });
}
async function screenshot(name, mobile = false) {
  await page.setViewportSize(
    mobile ? { width: 390, height: 844 } : { width: 1440, height: 1100 },
  );
  const modal = await dialog.isVisible();
  if (modal)
    await dialog.evaluate((el) => {
      el.scrollTop = 0;
    });
  await page.screenshot({
    path: `${dir}/beta3-${name}${mobile ? "-mobile" : ""}.png`,
    fullPage: !modal,
  });
  if (
    modal &&
    mobile &&
    (await dialog.evaluate((el) => el.scrollHeight > el.clientHeight + 10))
  ) {
    await dialog.evaluate((el) => {
      el.scrollTop = el.scrollHeight;
    });
    await page.screenshot({
      path: `${dir}/beta3-${name}-mobile-bottom.png`,
      fullPage: false,
    });
    await dialog.evaluate((el) => {
      el.scrollTop = 0;
    });
  }
}

try {
  for (const p of ["aws", "azure", "linode", "digitalocean"]) {
    await fresh(p);
    assert.equal(await page.locator("#region optgroup").count(), 4);
    assert.equal(
      await page
        .locator('#region optgroup[label="大洋洲"] option')
        .getAttribute("value"),
      regions()[2].id,
    );
    await page.locator("#region-search").fill("悉尼");
    if (p === "azure") await page.locator("#region-search").fill("新南威尔士");
    await page
      .locator('#region optgroup[label="大洋洲"] option')
      .waitFor({ state: "attached" });
    assert.equal(
      await page.locator("#region optgroup").count(),
      1,
      "Filtered region lost continent grouping",
    );
    await screenshot(`${p}-regions`);
    await page.locator("#region-search").fill("");
    await page.locator("#region").selectOption(regions()[0].id);
    assert.equal(await page.locator("#region").inputValue(), regions()[0].id);
    await page.locator("#create").click();
    await dialog
      .locator('[name="region"] optgroup')
      .first()
      .waitFor({ state: "attached" });
    assert.equal(await dialog.locator('[name="region"] optgroup').count(), 4);
    await dialog.locator('[name="regionSearch"]').fill(regions()[2].id);
    assert.equal(
      await dialog
        .locator('[name="region"] optgroup[label="大洋洲"] option')
        .getAttribute("value"),
      regions()[2].id,
    );
    if (p === "linode") {
      assert.equal(
        await dialog.locator('[name="firewallMode"]').inputValue(),
        "all",
      );
      await dialog
        .getByText("允许公网访问服务器上所有正在监听的端口", { exact: false })
        .waitFor();
    }
    await screenshot(`${p}-create-groups`);
    await close();
  }
  await fresh();
  await screenshot("servers", true);
  await page.setViewportSize({ width: 1440, height: 1100 });
  await details();
  for (const a of ["rebuild", "resize", "password-reset", "clone", "migrate"])
    assert.equal(await dialog.locator(`[data-advanced="${a}"]`).count(), 1);
  await screenshot("management");
  await screenshot("management", true);
  await page.setViewportSize({ width: 1440, height: 1100 });
  await dialog.locator('[data-detail="transfer"]').click();
  await dialog.getByText("账户共享池（当前月）", { exact: true }).waitFor();
  assert.match(await dialog.innerText(), /1 GiB/);
  assert.match(await dialog.innerText(), /5 GiB \/ 总额度 5,000 GiB/);
  assert.match(await dialog.innerText(), /不是独享剩余额度/);
  await screenshot("transfer");
  await screenshot("transfer", true);
  await dialog.locator("[data-period]").fill("2025-01");
  await dialog.locator("[data-query]").click();
  await dialog
    .getByText("历史月未查询当前账户共享池", { exact: false })
    .waitFor();
  assert.match(await dialog.innerText(), /2025-01/);
  assert.match(await dialog.innerText(), /3 GiB/);
  await dialog.locator('[data-detail="stats"]').click();
  await dialog.locator(".metric-chart svg").nth(3).waitFor();
  assert.equal(await dialog.locator(".metric-chart polyline").count(), 4);
  assert.match(await dialog.innerText(), /bits\/s/);
  assert.match(await dialog.innerText(), /Block\/s/);
  assert.match(await dialog.innerText(), /采样300秒/);
  assert.match(await dialog.innerText(), /过去24小时/);
  await screenshot("monitoring");
  await screenshot("monitoring", true);
  emptyStats = true;
  await dialog.locator("[data-query]").click();
  await dialog.getByText("暂无监控数据", { exact: true }).first().waitFor();
  assert.equal(await dialog.locator(".metric-chart svg").count(), 0);
  assert.doesNotMatch(await dialog.innerText(), /0\s*%/);
  await screenshot("monitoring-empty");
  emptyStats = false;
  await dialog
    .locator("[data-period]")
    .fill(new Date().toISOString().slice(0, 7));
  await dialog.locator("[data-query]").click();
  await dialog
    .getByText("过去30天（当前年月接口）", { exact: false })
    .waitFor();
  const delayed = delay("stats", "2025-01");
  await dialog.locator("[data-period]").fill("2025-01");
  await dialog.locator("[data-query]").click();
  await delayed;
  await dialog.locator("[data-period]").fill("2025-02");
  await dialog.locator("[data-query]").click();
  await dialog.getByText("2025-02", { exact: false }).waitFor();
  releaseDelayed();
  await page.waitForTimeout(100);
  assert.doesNotMatch(
    await dialog.locator("[data-result]").innerText(),
    /2025-01/,
  );
  failView = "stats";
  await dialog.locator("[data-query]").click();
  await dialog
    .getByText("缺少账单或资源读取权限（测试）", { exact: true })
    .waitFor();
  failView = "";
  await close();

  // Dedicated confirmation payloads: no start/stop endpoint shortcuts.
  await advanced("rebuild");
  await dialog
    .locator('[name="newImage"] option[value="linode/debian13"]')
    .waitFor({ state: "attached" });
  await dialog.locator('[name="newImage"]').selectOption("linode/debian13");
  await dialog
    .locator('[name="publicKey"]')
    .fill("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureForBrowserOnly");
  assert.match(await dialog.innerText(), /永久删除/);
  await screenshot("rebuild");
  let job = await completeAction("rebuild");
  assert.equal(job.target, "42");
  assert.equal(job.advanced.image, "linode/debian13");
  assert.equal(job.advanced.snapshot, "instance-42-snapshot");
  await advanced("resize");
  await dialog
    .locator('[name="newPlan"] option[value="g6-standard-2"]')
    .waitFor({ state: "attached" });
  await dialog.locator('[name="newPlan"]').selectOption("g6-standard-2");
  assert.match(
    await dialog.locator(".target-comparison").innerText(),
    /2 vCPU/,
  );
  assert.match(await dialog.innerText(), /不会自动缩小磁盘或删除数据/);
  await screenshot("resize");
  job = await completeAction("resize");
  assert.equal(job.advanced.plan, "g6-standard-2");
  await advanced("password-reset");
  assert.equal(
    await dialog.locator('[name="newDisk"] option[value="102"]').count(),
    0,
    "Swap offered as root filesystem",
  );
  await dialog.locator('[name="newDisk"]').selectOption("101");
  await dialog.locator('[name="password"]').fill("Fixture!R00tReset9x");
  assert.match(await dialog.innerText(), /不自动关机或开机/);
  job = await completeAction("password-reset");
  assert.equal(job.advanced.diskId, 101);
  assert.equal(job.advanced.password, "Fixture!R00tReset9x");
  await advanced("clone");
  await dialog
    .locator('[name="newRegion"] option[value="gb-lon"]')
    .waitFor({ state: "attached" });
  await dialog.locator('[name="newRegion"]').selectOption("gb-lon");
  await dialog
    .locator('[name="newPlan"] option[value="g6-standard-2"]')
    .waitFor({ state: "attached" });
  await dialog.locator('[name="newPlan"]').selectOption("g6-standard-2");
  await dialog.locator('[name="newLabel"]').fill("fixture-clone");
  assert.equal(
    await dialog.locator('[name="firewallMode"]').inputValue(),
    "all",
  );
  assert.match(await dialog.innerText(), /新的计费实例/);
  await screenshot("clone");
  job = await completeAction("clone");
  assert.equal(job.target, "42");
  assert.equal(job.region, "ap-northeast");
  assert.equal(job.advanced.region, "gb-lon");
  assert.equal(job.advanced.label, "fixture-clone");
  assert.equal(job.advanced.firewall.mode, "all");
  await advanced("migrate");
  await dialog
    .locator('[name="newRegion"] option[value="gb-lon"]')
    .waitFor({ state: "attached" });
  assert.equal(
    await dialog
      .locator('[name="newRegion"] option[value="ap-northeast"]')
      .count(),
    0,
  );
  await dialog.locator('[name="newRegion"]').selectOption("gb-lon");
  await dialog.locator('button[type="submit"]:enabled').waitFor();
  assert.match(await dialog.innerText(), /公网IPv4\/IPv6可能变化/);
  assert.match(await dialog.innerText(), /不会更改外部DNS/);
  await screenshot("migrate");
  job = await completeAction("migrate");
  assert.equal(job.advanced.region, "gb-lon");

  // Billing cannot disable unrelated management; unknown values cannot become $0.
  await fresh();
  billingDenied = true;
  await accountView("billing");
  await dialog
    .getByText("缺少这些权限不影响无关服务器管理", { exact: false })
    .waitFor();
  await close();
  await details();
  await dialog.locator('[data-advanced="resize"]').waitFor();
  billingDenied = false;
  await accountView("billing");
  await dialog.getByText("账户抵扣额度 25 USD", { exact: false }).waitFor();
  assert.match(await dialog.innerText(), /估算，不含流量/);
  assert.match(await dialog.innerText(), /付款状态未提供/);
  await screenshot("billing");
  await screenshot("billing", true);
  await dialog.locator('[data-invoice="9"]').click();
  await dialog.getByText("fixture-item-9", { exact: false }).waitFor();
  assert.match(await dialog.innerText(), /2.5 USD/);
  const invoiceDelay = delay("invoice", "9");
  await dialog.locator('[data-invoice="9"]').click();
  await invoiceDelay;
  await dialog.locator('[data-invoice="10"]').click();
  await dialog.getByText("fixture-item-10", { exact: false }).waitFor();
  releaseDelayed();
  await page.waitForTimeout(100);
  assert.doesNotMatch(
    await dialog.locator("[data-invoice-detail]").innerText(),
    /fixture-item-9/,
    "Stale invoice replaced most recent invoice selection",
  );
  await close();
  unknownBilling = true;
  await accountView("billing");
  await dialog.getByText("余额未提供", { exact: false }).waitFor();
  assert.match(await dialog.innerText(), /未出账估算 官方未提供/);
  assert.doesNotMatch(await dialog.innerText(), /未出账估算 0/);
  unknownBilling = false;
  await close();

  // Account firewall CRUD stays scoped; shared mutations show every binding.
  await accountView("firewalls");
  await dialog.locator('[data-rule-edit="7"]').waitFor();
  assert.equal(await dialog.locator('[data-delete-fw="7"]').isDisabled(), true);
  assert.match(await dialog.innerText(), /::\/0/);
  assert.match(await dialog.innerText(), /0\.0\.0\.0\/0/);
  await screenshot("firewalls");
  await screenshot("firewalls", true);
  await dialog.locator("[data-new-firewall]").click();
  await dialog.locator('[name="label"]').fill("fixture-created-fw");
  await dialog.locator("[data-add-rule]").click();
  await dialog.locator("[data-protocol]").selectOption("ICMP");
  assert.equal(await dialog.locator("[data-ports]").isDisabled(), true);
  await dialog.locator("[data-v4]").fill("0.0.0.0/0");
  await dialog.locator("[data-v6]").fill("::/0");
  job = await completeAction("firewall-create", "fixture-created-fw");
  assert.equal(job.target, "");
  assert.equal(job.advanced.rules.inbound_policy, "DROP");
  assert.equal(job.advanced.rules.outbound_policy, "ACCEPT");
  assert.equal(job.advanced.rules.inbound[0].protocol, "ICMP");
  assert.deepEqual(job.advanced.rules.inbound[0].addresses, {
    ipv4: ["0.0.0.0/0"],
    ipv6: ["::/0"],
  });
  await accountView("firewalls");
  await dialog.locator('[data-rule-edit="7"]').click();
  assert.match(await dialog.innerText(), /2 个绑定/);
  assert.match(await dialog.innerText(), /fixture-neighbor/);
  await dialog.locator("[data-add-rule]").click();
  await dialog.locator("[data-protocol]").last().selectOption("UDP");
  await dialog.locator("[data-ports]").last().fill("53");
  await screenshot("firewall-shared-rules");
  job = await completeAction("firewall-rules", "shared-fixture");
  assert.equal(job.target, "7");
  assert.equal(job.advanced.snapshot, "firewall-7-snapshot");
  assert.equal(job.advanced.rules.inbound.length, 2);
  await accountView("firewalls");
  await dialog.locator('[data-status-edit="7"]').click();
  job = await completeAction("firewall-status", "shared-fixture");
  assert.equal(job.target, "7");
  assert.equal(job.advanced.status, "disabled");
  await accountView("firewalls");
  await dialog.locator('[data-delete-fw="8"]').click();
  job = await completeAction("firewall-delete", "unused-fixture");
  assert.equal(job.target, "8");

  for (const gen of ["legacy_config", "linode"]) {
    generation = gen;
    await fresh();
    await details();
    await dialog.locator('[data-detail="firewall-bind"]').click();
    await dialog.locator(".advanced-form").waitFor();
    if (gen === "linode") {
      assert.equal(
        await dialog
          .locator('[name="newInterface"] option[value="57"]')
          .count(),
        0,
        "VLAN incorrectly offered a cloud firewall binding",
      );
      await dialog.locator('[name="newInterface"]').selectOption("55");
    } else
      assert.equal(await dialog.locator('[name="newInterface"]').count(), 0);
    await dialog.locator('[name="firewallMode"]').selectOption("existing");
    await dialog
      .locator('[name="firewallID"] option[value="7"]')
      .waitFor({ state: "attached" });
    await dialog.locator('[name="firewallID"]').selectOption("7");
    await screenshot(`firewall-bind-${gen}`);
    job = await completeAction("firewall-bind");
    assert.equal(job.advanced.interfaceId, gen === "linode" ? 55 : 0);
    assert.equal(
      job.advanced.bindingSnapshot,
      gen === "linode" ? "binding-55" : "binding-legacy",
    );
    assert.equal(job.advanced.firewall.id, 7);
  }
  generation = "legacy_config";

  for (const mode of ["all", "existing", "custom"]) {
    await fresh();
    await page.locator("#create").click();
    const form = dialog.locator("#create-form");
    await form
      .locator('[name="region"] option[value="ap-northeast"]')
      .waitFor({ state: "attached" });
    await form.locator('[name="region"]').selectOption("ap-northeast");
    await form
      .locator('[name="plan"] option[value="g6-standard-1"]')
      .waitFor({ state: "attached" });
    await form.locator('[name="plan"]').selectOption("g6-standard-1");
    await form.locator('[name="image"]').selectOption("linode/debian13");
    await form.locator('[name="name"]').fill(`fixture-created-${mode}`);
    await form
      .locator('[name="sshPublicKey"]')
      .fill("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureForBrowserOnly");
    await form.locator('[name="firewallMode"]').selectOption(mode);
    if (mode === "existing") {
      await form
        .locator('[name="firewallID"] option[value="7"]')
        .waitFor({ state: "attached" });
      await form.locator('[name="firewallID"]').selectOption("7");
    }
    if (mode === "custom") {
      await form.locator("[data-add-rule]").click();
      await form.locator("[data-protocol]").selectOption("TCP");
      await form.locator("[data-ports]").fill("22,443");
      await form.locator("[data-v4]").fill("0.0.0.0/0");
      await form.locator("[data-v6]").fill("::/0");
    }
    await form.locator('[name="confirmName"]').fill(`fixture-created-${mode}`);
    const before = jobs.length;
    await form.locator('button[type="submit"]').click();
    await dialog.waitFor({ state: "hidden" });
    assert.equal(jobs.length, before + 1);
    const created = jobs.at(-1);
    assert.equal(created.action, "create");
    assert.equal(created.spec.region, "ap-northeast");
    assert.equal(created.spec.firewall.mode, mode);
    if (mode === "existing") assert.equal(created.spec.firewall.id, 7);
    if (mode === "custom")
      assert.deepEqual(created.spec.firewall.rules.inbound[0].addresses, {
        ipv4: ["0.0.0.0/0"],
        ipv6: ["::/0"],
      });
  }

  // A region response arriving after a later selection cannot replace its price.
  await advanced("clone");
  await dialog
    .locator('[name="newRegion"] option[value="gb-lon"]')
    .waitFor({ state: "attached" });
  catalogDelayRegion = "gb-lon";
  const slowCatalog = new Promise((yes) => {
    catalogDelayStarted = yes;
  });
  await dialog.locator('[name="newRegion"]').selectOption("gb-lon");
  await slowCatalog;
  await dialog.locator('[name="newRegion"]').selectOption("ap-southeast");
  await dialog
    .locator('[name="newPlan"] option[value="g6-standard-2"]')
    .waitFor({ state: "attached" });
  await dialog.locator('[name="newPlan"]').selectOption("g6-standard-2");
  releaseCatalog();
  await page.waitForTimeout(100);
  assert.equal(
    await dialog.locator('[name="newRegion"]').inputValue(),
    "ap-southeast",
  );
  assert.match(
    await dialog.locator(".target-comparison").innerText(),
    /24 USD/,
  );
  assert.doesNotMatch(
    await dialog.locator(".target-comparison").innerText(),
    /25 USD/,
  );
  await close();

  await fresh();
  await details();
  catalogDenied = true;
  await dialog.locator('[data-advanced="clone"]').click();
  await dialog.getByText("测试目录暂不可用，请重试", { exact: true }).waitFor();
  assert.equal(
    await dialog.locator('button[type="submit"]').isDisabled(),
    true,
  );
  catalogDenied = false;
  await close();

  // A malformed or failed optional view must be visible and recoverable.
  await fresh();
  emptyView = "stats";
  await details();
  await dialog.locator('[data-detail="stats"]').click();
  await dialog.locator(".error-box").filter({ hasText: /./ }).waitFor();
  assert.equal(await dialog.locator(".metric-chart polyline").count(), 0);
  emptyView = "";
  await close();
  failView = "instance";
  await page.locator('[data-nav="instances"]').click();
  await page.locator("[data-details]").first().click();
  await dialog.locator("[data-retry]").waitFor();
  failView = "";
  await dialog.locator("[data-retry]").click();
  await dialog.locator('[data-advanced="rebuild"]').waitFor();
  await close();

  // Terminal advanced jobs invalidate the cached inventory before returning to
  // the server list; refreshed system/spec/location come from provider reads.
  for (const action of ["rebuild", "resize", "migrate"]) {
    jobResponses = [
      {
        id: `fixture-terminal-${action}`,
        accountId: "fixture",
        action,
        name: "fixture-source",
        region: "ap-northeast",
        state: "queued",
        created: 1780000000,
        progress: "等待执行",
      },
    ];
    instanceOverrides = {};
    await fresh();
    await page.locator('[data-nav="jobs"]').click();
    await page.locator("#refresh-jobs").click();
    await page.getByText("等待执行", { exact: false }).first().waitFor();
    instanceOverrides =
      action === "rebuild"
        ? { image: "linode/ubuntu24.04" }
        : action === "resize"
          ? { plan: "g6-standard-2", cpu: 2, memory: 4096, disk: 80 }
          : {
              region: "gb-lon",
              regionName: "英国伦敦 2 · gb-lon",
              ips: ["8.8.4.4", "2001:4860::84"],
            };
    jobResponses = jobResponses.map((j) => ({
      ...j,
      state: "succeeded",
      progress: "云端已核实完成",
    }));
    const refreshed = page.waitForRequest((r) => {
      const u = new URL(r.url());
      return (
        r.method() === "GET" &&
        u.pathname === "/api/inventory" &&
        u.searchParams.get("accountId") === "fixture"
      );
    });
    await page.locator("#refresh-jobs").click();
    await refreshed;
    await page.getByText("云端已核实完成", { exact: false }).waitFor();
    await page.locator('[data-nav="instances"]').click();
    await page.locator("[data-details]").first().waitFor();
    if (action === "resize") {
      await page.locator("td").filter({ hasText: "g6-standard-2" }).waitFor();
      assert.match(await page.locator("tbody").innerText(), /2 vCPU \/ 4 GB/);
    } else if (action === "migrate") {
      assert.match(await page.locator("tbody").innerText(), /gb-lon/);
      assert.match(await page.locator("tbody").innerText(), /8\.8\.4\.4/);
    } else {
      await page.locator("[data-details]").first().click();
      await dialog.getByText("linode/ubuntu24.04", { exact: true }).waitFor();
      await close();
    }
  }
  jobResponses = [];
  instanceOverrides = {};
  assert.deepEqual(errors, [], "Browser/fixture errors occurred");
  console.log(
    JSON.stringify({
      result: "PASS",
      evidence: "browser-fixtures-only",
      cloudWrites: 0,
      mockedConfirmations: confirms.length,
      mockedJobs: jobs.length,
      reads: requests.filter((r) => r.method === "GET").length,
      features: [
        "rebuild",
        "resize",
        "password-reset",
        "clone",
        "migrate",
        "transfer",
        "stats",
        "billing",
        "firewall",
      ],
      screenshots: dir,
    }),
  );
} finally {
  releaseDelayed?.();
  releaseCatalog?.();
  await browser.close();
}
