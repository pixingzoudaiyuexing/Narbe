import { chromium } from "playwright";
import assert from "node:assert/strict";
import fs from "node:fs";
import crypto from "node:crypto";
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
const page = await browser.newPage({ viewport: { width: 1440, height: 1080 } }),
  errors = [],
  writes = [],
  dir = process.env.NARBE_SCREENSHOT_DIR || "test-results";
fs.mkdirSync(dir, { recursive: true });
page.on("pageerror", (e) => errors.push(e.message));
let provider = "aws",
  denied = false,
  stackVersion = "version-1",
  delayID = 0,
  started,
  release,
  loseJob = false;
const regions = [
  {
    id: "r1",
    name: "日本东京 · r1",
    continent: "亚洲",
    extra: { metadata: "true", siteType: "core" },
  },
  {
    id: "r2",
    name: "英国伦敦 · r2",
    continent: "欧洲",
    extra: { metadata: "true", siteType: "core" },
  },
];
const images = [
  {
    id: "linux",
    name: "Ubuntu 24.04 LTS",
    family: "ubuntu",
    architecture: "x86_64",
    disk: 10,
    startupSupport: "cloud-init",
  },
  {
    id: "no-init",
    name: "无Cloud-init镜像",
    family: "debian",
    architecture: "x86_64",
    disk: 10,
    startupSupport: "unsupported",
  },
  {
    id: "windows",
    name: "Windows Server 2022",
    family: "windows",
    architecture: "x86_64",
    disk: 10,
  },
];
function script(id) {
  return {
    id,
    label: id === 7 ? "初始化 <img src=x onerror=alert(1)>" : "Another script",
    description: "说明 <script>window.injected=true</script>",
    mine: id === 7,
    is_public: id !== 7,
    username: "fixture",
    images: ["linux"],
    snapshot: stackVersion,
    user_defined_fields: [
      {
        name: "MESSAGE",
        label: "欢迎文字 <img onerror=alert(1)>",
        example: "保留引号与Unicode",
        default: null,
      },
      { name: "SITE", label: "地区", oneOf: "east,west", default: "east" },
      { name: "TOOLS", label: "工具", manyOf: "a,b,c", default: "a" },
      { name: "PASSWORD", label: "应用密码", default: "" },
      ...(id === 8
        ? [
            { name: "__proto__", label: "合法原型同名参数", default: "" },
            { name: "constructor", label: "构造同名参数", default: "" },
            {
              name: "toString",
              label: "字符串同名多选",
              manyOf: "a,b",
              default: "a",
            },
          ]
        : []),
    ],
  };
}
await page.route("**/*", async (route) => {
  const req = route.request(),
    u = new URL(req.url());
  if (u.origin !== url.origin) {
    errors.push("external request");
    return route.abort();
  }
  if (!u.pathname.startsWith("/api/")) return route.continue();
  const path = u.pathname.slice(5),
    aid = u.searchParams.get("accountId") || "a";
  let body;
  if (req.method() !== "GET") {
    assert.equal(req.method(), "POST");
    assert.ok(["confirm", "jobs"].includes(path), "unexpected mutation");
    const b = req.postDataJSON();
    assert.equal(b.accountId, "a");
    writes.push({ path, body: b });
    if (path === "confirm")
      return route.fulfill({
        json: { confirmation: "safe-" + b.id, accountRevision: 1 },
      });
    if (loseJob) {
      loseJob = false;
      return route.abort("failed");
    }
    return route.fulfill({ status: 202, json: { id: b.id } });
  }
  if (path === "session") body = { csrf: "fixture-csrf" };
  else if (path === "accounts")
    body = [
      { id: "a", name: "脚本测试账号", provider, revision: 1, created: 1 },
      { id: "b", name: "第二账号", provider, revision: 1, created: 2 },
    ];
  else if (path === "jobs") body = [];
  else if (path === "inventory")
    body = {
      accountId: aid,
      accountRevision: 1,
      phase: "ready",
      complete: true,
      items: [],
      regions,
      failures: [],
      warnings: [],
      succeeded: ["r1", "r2"],
      scope: "当前账号全部地区",
      stats: { total: 0, running: 0, stopped: 0, other: 0 },
      updatedAt: 1780000000,
    };
  else if (path === "catalog")
    body = {
      regions,
      plans: u.searchParams.get("region")
        ? [
            {
              id: "p",
              name: "small",
              cpu: 2,
              memory: 4096,
              disk: 50,
              architecture: "x86_64",
            },
          ]
        : [],
      images: u.searchParams.get("region") ? images : [],
      keys: [{ id: "key", name: "SSH密钥", extra: { type: "rsa" } }],
      networks: [{ id: "net", name: "子网" }],
      groups: [{ id: "group", name: "安全组" }],
    };
  else if (path === "linode")
    body = {
      accountId: aid,
      accountRevision: 1,
      data: { items: [], snapshot: "firewall-list-fixture" },
    };
  else if (path === "stackscripts") {
    if (denied)
      return route.fulfill({
        status: 403,
        json: { error: "需要stackscripts:read_only权限" },
      });
    const id = Number(u.searchParams.get("id"));
    if (id && id === delayID) {
      delayID = 0;
      started?.();
      await new Promise((resolve) => (release = resolve));
    }
    body = {
      accountId: aid,
      accountRevision: 1,
      data: id
        ? script(id)
        : { items: [script(7), script(8)], page: 1, pages: 1, results: 2 },
    };
  } else throw Error("unexpected API " + path);
  await route.fulfill({ json: body });
});
const f = () => page.locator("#create-form");
async function fresh(p) {
  provider = p;
  await page.goto(url.href);
  await page.locator("#create").waitFor();
  await page.locator("#create").click();
  await f().locator("[name=region]").selectOption("r1");
  await f()
    .locator("[name=plan] option[value=p]")
    .waitFor({ state: "attached" });
  await f().locator("[name=plan]").selectOption("p");
  await f().locator("[name=image]").selectOption("linux");
  await f().locator("[name=name]").fill("startup-fixture");
  assert.equal(await f().locator("[name=confirmName]").count(), 0);
  if (p === "aws") {
    await f().locator("[name=network]").selectOption("net");
    await f().locator("[name=group]").selectOption("group");
  }
  if (p === "azure") {
    await f().locator("[name=network]").selectOption("net");
    await f().locator("[name=managementCidr]").fill("192.0.2.1/32");
    await f().locator("[name=sshPublicKey]").fill("ssh-ed25519 fixture");
  }
  if (p === "digitalocean" || p === "aws")
    await f().locator("[name=key]").selectOption("key");
  if (p === "linode")
    await f().locator("[name=password]").fill("Fixture-Password-Secret-83!");
}
async function mode(value, text) {
  await f().locator("[name=scriptMode]").selectOption(value);
  if (text !== undefined) await f().locator("[name=userData]").fill(text);
  const ack = f().locator("[name=scriptCompatibilityAck]");
  if (await ack.isVisible()) await ack.check();
}
async function shot(name) {
  await page.screenshot({ path: `${dir}/beta4-${name}-desktop.png` });
  await page.setViewportSize({ width: 390, height: 844 });
  await f().locator(".startup-fields").scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${dir}/beta4-${name}-mobile.png` });
  await page.setViewportSize({ width: 1440, height: 1080 });
}
try {
  for (const p of ["aws", "azure", "linode", "digitalocean"]) {
    await fresh(p);
    assert.equal(await f().locator("[name=scriptMode]").inputValue(), "none");
    const raw =
      '#!/bin/bash\nprintf \'%s\\n\' "中文 \\"quoted\\" $HOME <>& 💻"\n';
    await mode("shell", raw);
    const hash = crypto.createHash("sha256").update(raw).digest("hex");
    assert.match(
      await f().locator("#create-summary").innerText(),
      new RegExp(hash),
    );
    assert.ok(
      !(await f().locator("#create-summary").innerText()).includes(raw),
    );
    assert.ok(
      (await f().locator("[data-startup-bytes]").innerText()).includes(
        String(Buffer.byteLength(raw)),
      ),
    );
    await mode("cloud-config", '#cloud-config\nruncmd:\n  - echo "保留"\n');
    await f().locator("[name=userData]").scrollIntoViewIfNeeded();
    await shot(p + "-cloud-config");
    await mode("shell");
    assert.equal(
      await f().locator("[name=userData]").inputValue(),
      raw,
      "mode switch lost draft",
    );
    await f().locator("[name=region]").selectOption("r2");
    await f()
      .locator("[name=plan] option[value=p]")
      .waitFor({ state: "attached" });
    await f().locator("[name=plan]").selectOption("p");
    await f().locator("[name=image]").selectOption("linux");
    assert.equal(
      await f().locator("[name=userData]").inputValue(),
      raw,
      "catalog reload lost script",
    );
    if (p !== "linode")
      await f().locator("[name=scriptCompatibilityAck]").check();
    if (p === "aws") {
      await f().locator("[name=network]").selectOption("net");
      await f().locator("[name=group]").selectOption("group");
      await f().locator("[name=key]").selectOption("key");
    }
    if (p === "azure") {
      await f().locator("[name=network]").selectOption("net");
      await f().locator("[name=managementCidr]").fill("192.0.2.1/32");
      await f().locator("[name=sshPublicKey]").fill("ssh-ed25519 fixture");
    }
    if (p === "digitalocean")
      await f().locator("[name=key]").selectOption("key");
    if (p === "linode")
      await f().locator("[name=password]").fill("Fixture-Password-Secret-83!");
    if (p === "linode") {
      await f().locator("[name=image]").selectOption("no-init");
      assert.equal(await f().locator("[type=submit]").isDisabled(), true);
      assert.match(await f().innerText(), /未确认Cloud-init/);
      await f().locator("[name=image]").selectOption("linux");
    }
    await f().locator("[name=userData]").scrollIntoViewIfNeeded();
    await shot(p + "-shell");
    const before = writes.length;
    if (p === "aws") loseJob = true;
    await f().locator("[type=submit]").click();
    if (p === "aws") {
      await f()
        .locator(".error-box")
        .first()
        .getByText("连接中断", { exact: false })
        .waitFor();
      const ids = writes.slice(before).map((w) => w.body.id);
      await f().locator("[type=submit]").click();
      await page
        .getByRole("heading", { name: "操作任务", exact: true })
        .waitFor();
      assert.equal(
        new Set([...ids, ...writes.slice(before).map((w) => w.body.id)]).size,
        1,
        "script retry replaced intent",
      );
    } else
      await page
        .getByRole("heading", { name: "操作任务", exact: true })
        .waitFor();
    const sent = writes.findLast((w) => w.path === "jobs").body.spec;
    assert.equal(sent.userData, raw);
    assert.equal(sent.scriptMode, "shell");
    if (p === "linode") assert.equal(sent.firewall.mode, "all");
  }
  await fresh("aws");
  await f().locator("[name=image]").selectOption("windows");
  await mode("powershell", 'Write-Output "中文 $env:USERNAME"\n');
  await shot("aws-powershell");
  await f().locator("[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  assert.equal(writes.at(-1).body.spec.scriptMode, "powershell");
  await fresh("azure");
  await mode("shell", "#!/bin/bash\necho original\n");
  await f().locator("[name=image]").selectOption("windows");
  assert.equal(await f().locator("[type=submit]").isDisabled(), true);
  assert.match(await f().innerText(), /不会自动执行/);
  await f().locator("[name=image]").selectOption("linux");
  assert.equal(
    await f().locator("[name=userData]").inputValue(),
    "#!/bin/bash\necho original\n",
  );
  await fresh("linode");
  await mode("stackscript");
  await f()
    .locator('[name=stackID] option[value="7"]')
    .waitFor({ state: "attached" });
  await f().locator("[name=stackID]").selectOption("7");
  await f().locator('[data-udf="0"]').waitFor();
  await f().locator('[data-udf="0"]').fill('中文 "quotes" <>& 💻');
  await f().locator('[data-udf="2"]').selectOption(["a", "c"]);
  await f().locator('[data-udf="3"]').fill("sensitive-value");
  assert.equal(await page.evaluate(() => !!window.injected), false);
  assert.equal(await f().locator("[data-stack-description] script").count(), 0);
  assert.equal(await f().locator("[data-stack-udf] img").count(), 0);
  await shot("linode-stackscript");
  await f().locator("[type=submit]").click();
  await page.getByRole("heading", { name: "操作任务", exact: true }).waitFor();
  const ss = writes.at(-1).body.spec;
  assert.equal(ss.stackscriptId, 7);
  assert.equal(ss.stackscriptData.MESSAGE, '中文 "quotes" <>& 💻');
  assert.equal(ss.stackscriptData.TOOLS, "a,c");
  assert.equal(ss.userData, "");
  assert.equal(ss.firewall.mode, "all");
  await fresh("linode");
  denied = true;
  await mode("stackscript");
  await f()
    .locator("[data-startup-error]")
    .getByText("stackscripts:read_only", { exact: false })
    .waitFor();
  assert.equal(await f().locator("[type=submit]").isDisabled(), true);
  denied = false;
  await f().locator("[data-stack-search]").click();
  await f()
    .locator('[name=stackID] option[value="8"]')
    .waitFor({ state: "attached" });
  await f().locator("[name=stackID]").selectOption("7");
  await f().locator('[data-udf="0"]').waitFor();
  await f().locator('[data-udf="0"]').fill("持久草稿A");
  await f().locator("[name=stackID]").selectOption("8");
  await f()
    .locator("[data-stack-status]")
    .getByText("公共脚本", { exact: false })
    .waitFor();
  await f().locator("[name=stackID]").selectOption("7");
  await f()
    .locator("[data-stack-status]")
    .getByText("我的脚本", { exact: false })
    .waitFor();
  assert.equal(
    await f().locator('[data-udf="0"]').inputValue(),
    "持久草稿A",
    "switching scripts erased UDF draft",
  );
  const began = new Promise((resolve) => (started = resolve));
  delayID = 7;
  await f().locator("[name=stackID]").selectOption("7");
  await began;
  await f().locator("[name=stackID]").selectOption("8");
  await f()
    .locator("[data-stack-status]")
    .getByText("公共脚本", { exact: false })
    .waitFor();
  release();
  await page.waitForTimeout(80);
  assert.match(
    await f().locator("[data-stack-description]").innerText(),
    /说明/,
  );
  assert.ok(
    (await f().locator("[data-stack-status]").innerText()).includes("公共脚本"),
    "late private script overwrote public selection",
  );
  await f().locator('[data-udf="4"]').fill("proto-value");
  await f().locator('[data-udf="5"]').fill("constructor-value");
  assert.deepEqual(
    await f()
      .locator('[data-udf="6"]')
      .evaluate((el) => Array.from(el.selectedOptions, (o) => o.value)),
    ["a"],
  );
  const cancelled = new Promise((resolve) => (started = resolve));
  delayID = 7;
  await f().locator("[name=stackID]").selectOption("7");
  await cancelled;
  await f().locator("[name=stackID]").selectOption("");
  release();
  await page.waitForTimeout(100);
  assert.equal(await f().locator("[name=stackID]").inputValue(), "");
  assert.equal(
    await f().locator("[type=submit]").isDisabled(),
    true,
    "cleared StackScript revived by late response",
  );
  assert.equal(
    await f().locator("[data-udf]").count(),
    0,
    "cancelled script fields revived",
  );
  await mode("none");
  assert.equal(await f().locator("[data-udf]:not(:disabled)").count(), 0);
  await page.locator("dialog header [data-close]").click();
  await page.locator("#create").click();
  assert.equal(
    await f().locator("[name=userData]").inputValue(),
    "",
    "closed script persisted",
  );
  assert.equal(
    await page.evaluate(() => localStorage.length + sessionStorage.length),
    0,
    "script stored in browser",
  );
  await page.locator("dialog header [data-close]").click();
  await page.locator("#account").selectOption("b");
  await page.locator("#create").click();
  await f().locator("[name=region]").selectOption("r1");
  await f()
    .locator("[name=plan] option[value=p]")
    .waitFor({ state: "attached" });
  await f().locator("[name=plan]").selectOption("p");
  await f().locator("[name=image]").selectOption("linux");
  await mode("stackscript");
  await f()
    .locator('[name=stackID] option[value="7"]')
    .waitFor({ state: "attached" });
  const accountWait = new Promise((resolve) => (started = resolve));
  delayID = 7;
  await f().locator("[name=stackID]").selectOption("7");
  await accountWait;
  await page.locator("dialog header [data-close]").click();
  await page.locator("#account").selectOption("a");
  await page.locator("#create").click();
  release();
  await page.waitForTimeout(80);
  assert.equal(await f().locator("[name=scriptMode]").inputValue(), "none");
  assert.equal(
    await f().locator("[data-udf]").count(),
    0,
    "other-account response leaked into new dialog",
  );
  assert.deepEqual(errors, []);
  console.log(
    JSON.stringify({
      result: "PASS",
      providers: 4,
      mockedJobs: writes.filter((w) => w.path === "jobs").length,
      cloudWrites: 0,
      unicodePreserved: true,
      lateStackResponseIgnored: true,
      scriptNotPersisted: true,
    }),
  );
} finally {
  release?.();
  await browser.close();
}
