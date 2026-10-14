const root = document.querySelector("#root"),
  dialog = document.querySelector("#dialog");
const state = {
  csrf: "",
  tab: "instances",
  accounts: [],
  accountId: "",
  region: "",
  catalog: null,
  instances: [],
  jobs: [],
  error: "",
  loading: false,
  generation: 0,
};
const providers = {
  aws: "AWS",
  azure: "Azure",
  linode: "Linode",
  digitalocean: "DigitalOcean",
};
const labels = {
  create: "创建服务器",
  start: "开机",
  stop: "关机",
  poweroff: "关机并保留分配",
  reboot: "重启",
  delete: "删除服务器",
};
const statuses = {
  running: "运行中",
  active: "运行中",
  on: "运行中",
  stopped: "已关机",
  off: "已关机",
  offline: "已关机",
  deallocated: "已释放计算",
  queued: "等待执行",
  succeeded: "已完成",
  failed: "失败",
  unknown: "结果待核实",
  cancelled: "已取消",
  resolved: "已人工核实",
  new: "创建中",
  provisioning: "创建中",
  starting: "启动中",
  stopping: "关机中",
  rebooting: "重启中",
  deleting: "删除中",
};
const esc = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
const account = () => state.accounts.find((a) => a.id === state.accountId);
const date = (t) =>
  new Date(t * 1000).toLocaleString("zh-CN", { hour12: false });
function badge(s) {
  let cls = ["running", "active", "on", "succeeded"].includes(s)
    ? "good"
    : ["failed"].includes(s)
      ? "bad"
      : ["unknown", "queued"].includes(s)
        ? "warn"
        : "";
  return `<span class="status ${cls}">${esc(statuses[s] || s)}</span>`;
}
function toast(s) {
  document.querySelector("#toast").textContent = s;
  setTimeout(() => (document.querySelector("#toast").textContent = ""), 4500);
}
async function api(path, { method = "GET", body } = {}) {
  let res;
  try {
    res = await fetch("/api/" + path, {
      method,
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": state.csrf,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw Error(
      "连接中断。请使用当前表单重试，系统会核实同一任务，避免重复操作。",
    );
  }

  let data;
  try {
    data = await res.json();
  } catch {
    throw Error("响应未完成，请稍后重试");
  }
  if (!res.ok) {
    if (res.status === 401 && path !== "login") {
      state.csrf = "";
      state.generation++;
      login();
    }
    throw Error(data.error || "请求失败，请稍后重试");
  }
  return data;
}
function options(items, value = "", placeholder = "请选择") {
  return (
    `<option value="">${placeholder}</option>` +
    (items || [])
      .map(
        (i) =>
          `<option value="${esc(i.id)}" ${i.id === value ? "selected" : ""}>${esc(i.name)}${i.cpu ? ` · ${i.cpu} 核 / ${Math.round((i.memory / 1024) * 10) / 10} GB` : ""}${i.architecture ? " · " + esc(i.architecture) : ""}${i.family ? " · " + esc({ standard: "标准维护", lts: "LTS 安全维护", extended: "扩展安全维护", ended: "维护已结束", unreleased: "尚未发布", unknown: "维护期未确认" }[i.maintenance || "unknown"]) : ""}</option>`,
      )
      .join("")
  );
}
function login() {
  if (dialog.open) dialog.close();
  dialog.innerHTML = "";
  root.innerHTML = `<main class="login"><section class="login-brand"><div class="logo"><span class="mark">N</span>Narbe</div><div><h1>把你的云服务器，<br>放在同一个工作台。</h1><p>查看状态、选择镜像、管理服务器。<br>从一次清楚的操作开始。</p></div><div class="providers">${Object.values(
    providers,
  )
    .map((p) => `<span>${p}</span>`)
    .join(
      "",
    )}</div></section><section class="login-form"><form id="login" class="login-inner"><h2>登录工作台</h2><p class="sub">使用部署时设置的管理员密码。</p><div class="error-box" role="alert"></div><label class="field">管理员密码<input type="password" name="password" autocomplete="current-password" required minlength="12" maxlength="256" autofocus></label><button class="primary" type="submit">登录 Narbe →</button><p class="footer-note">请通过可信网络访问。远程使用时应由 HTTPS 保护连接。</p></form></section></main>`;
  document.querySelector("#login").onsubmit = async (e) => {
    e.preventDefault();
    const form = e.currentTarget;
    await formAction(form, async () => {
      const r = await api("login", {
        method: "POST",
        body: { password: form.password.value },
      });
      state.csrf = r.csrf;
      form.reset();
      await loadAccounts();
      shell();
    });
  };
}
async function formAction(form, fn) {
  const btn = form.querySelector("button[type=submit]");
  const box = form.querySelector(".error-box");
  if (box) box.textContent = "";
  if (btn) btn.disabled = true;
  try {
    await fn();
  } catch (e) {
    if (box) box.textContent = e.message;
    else toast(e.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}
async function loadAccounts() {
  state.accounts = await api("accounts");
  if (!state.accounts.some((a) => a.id === state.accountId)) {
    state.accountId = state.accounts[0]?.id || "";
    state.region = "";
    state.catalog = null;
    state.instances = [];
  }
}
function shell() {
  root.innerHTML = `<div class="layout"><aside class="sidebar"><div class="logo"><span class="mark">N</span>Narbe</div><div class="caption">云服务器工作台</div><nav class="nav" aria-label="主导航">${[
    ["instances", "▦", "服务器"],
    ["accounts", "◈", "云账号"],
    ["jobs", "◷", "操作任务"],
    ["settings", "⚙", "设置"],
  ]
    .map(
      ([id, icon, name]) =>
        `<button data-nav="${id}" class="${state.tab === id ? "active" : ""}"><span aria-hidden="true">${icon}</span>${name}</button>`,
    )
    .join(
      "",
    )}</nav><div class="sidebar-footer">单用户工作台<br>你的账号，你的资源</div></aside><main class="main"><header class="topbar"><span><i class="local-dot"></i>个人工作空间</span><button class="quiet" id="logout">退出登录 ↗</button></header><section id="content"></section></main></div>`;
  document.querySelectorAll("[data-nav]").forEach(
    (b) =>
      (b.onclick = () => {
        state.tab = b.dataset.nav;
        state.error = "";
        shell();
      }),
  );
  document.querySelector("#logout").onclick = async () => {
    try {
      await api("logout", { method: "POST" });
      state.csrf = "";
      state.generation++;
      state.accounts = [];
      state.jobs = [];
      login();
    } catch (e) {
      toast(e.message);
    }
  };
  render();
}
function heading(title, sub, button = "") {
  return `<div class="page-head"><div><h1>${title}</h1><p class="sub">${sub}</p></div>${button}</div>`;
}
function render() {
  if (!state.csrf) return;
  const content = document.querySelector("#content");
  if (!content) return;
  document
    .querySelectorAll("[data-nav]")
    .forEach((b) => b.classList.toggle("active", b.dataset.nav === state.tab));
  if (state.tab === "accounts") renderAccounts(content);
  else if (state.tab === "jobs") renderJobs(content);
  else if (state.tab === "settings") renderSettings(content);
  else renderInstances(content);
}
function renderInstances(el) {
  el.innerHTML =
    heading(
      "服务器",
      "查看资源状态，管理每一次云端操作。",
      `<button class="primary" id="create" ${!state.accountId ? "disabled" : ""}>＋ 创建服务器</button>`,
    ) +
    `${
      state.accounts.length
        ? `<div class="toolbar"><label>云账号<select id="account">${options(
            state.accounts.map((a) => ({
              id: a.id,
              name: providers[a.provider] + " · " + a.name,
            })),
            state.accountId,
            "选择账号",
          )}</select></label><label>地区<select id="region" ${!state.catalog ? "disabled" : ""}>${options(state.catalog?.regions, state.region, "选择地区")}</select></label><span class="grow"></span><button id="refresh" ${state.loading ? "disabled" : ""}>${state.loading ? "正在刷新…" : "↻ 刷新资源"}</button></div>`
        : ""
    }<div class="error-box" role="alert">${esc(state.error)}</div>${state.jobs.some((j) => j.state === "unknown") ? '<div class="notice">有操作结果待核实。请到「操作任务」检查云端结果后再继续。</div>' : ""}<section class="panel"><div class="panel-head"><h2>实例列表<span class="count">${state.instances.length}</span></h2><span class="help">${account() ? esc(providers[account().provider]) : "尚未接入云账号"}</span></div>${state.loading ? '<div class="loading" role="status">正在从云账号查询资源…</div>' : !state.accounts.length ? '<div class="empty"><div class="empty-icon">◈</div><h2>先接入你的第一个云账号</h2><p>连接 AWS、Azure、Linode 或 DigitalOcean，<br>即可在这里查看和管理服务器。</p><button class="primary" id="connect">接入云账号 →</button></div>' : !state.region ? '<div class="empty"><div class="empty-icon">⌖</div><h2>选择一个地区</h2><p>先刷新账号目录，再选择地区查看服务器。</p><button id="load-regions">加载可用地区</button></div>' : !state.instances.length ? '<div class="empty"><div class="empty-icon">▦</div><h2>这个地区还没有服务器</h2><p>可以创建新服务器，或切换其他地区。<br>创建前会展示配置并要求确认。</p><button class="primary" id="empty-create">创建服务器</button></div>' : `<p class="scroll-hint">← 左右滑动表格查看 IP 与操作 →</p><div class="table-wrap"><table><thead><tr><th>服务器</th><th>状态</th><th>公网 IP</th><th>规格 / 地区</th><th>操作</th></tr></thead><tbody>${state.instances.map((i, n) => `<tr><td><div class="name">${esc(i.name)}</div><div class="secondary mono" title="${esc(i.id)}">${esc(i.id)}</div></td><td>${badge(i.state)}</td><td>${i.ips?.length ? i.ips.map((ip) => `<div class="mono">${esc(ip)}</div>`).join("") : '<span class="help">未分配</span>'}</td><td>${esc(i.plan)}<div class="secondary">${esc(i.region)}</div></td><td><div class="row-actions"><select aria-label="${esc(i.name)} 的操作" data-action-select="${n}">${i.actions.map((a) => `<option value="${a}">${esc(labels[a])}</option>`).join("")}</select><button data-action="${n}">执行</button></div></td></tr>`).join("")}</tbody></table></div>`}</section><p class="footer-note">状态来自云厂商。停止实例后，磁盘、网络和保留 IP 仍可能计费。${account()?.provider === "azure" ? "Azure 删除仅删除虚拟机，保留磁盘、网卡与公网 IP。" : ""}</p>`;
  el.querySelector("#create")?.addEventListener("click", createDialog);
  el.querySelector("#empty-create")?.addEventListener("click", createDialog);
  el.querySelector("#connect")?.addEventListener("click", () =>
    accountDialog(),
  );
  el.querySelector("#load-regions")?.addEventListener("click", () =>
    refresh(true),
  );
  el.querySelector("#refresh")?.addEventListener("click", () =>
    refresh(!state.catalog),
  );
  el.querySelector("#account")?.addEventListener("change", (e) => {
    state.accountId = e.target.value;
    state.region = "";
    state.catalog = null;
    state.instances = [];
    refresh(true);
  });
  el.querySelector("#region")?.addEventListener("change", (e) => {
    state.region = e.target.value;
    refresh();
  });
  el.querySelectorAll("[data-action]").forEach(
    (b) =>
      (b.onclick = () => {
        const n = +b.dataset.action;
        actionDialog(
          state.instances[n],
          el.querySelector(`[data-action-select="${n}"]`).value,
        );
      }),
  );
}
async function refresh(catalog = false) {
  const gen = ++state.generation,
    aid = state.accountId,
    region = state.region;
  state.loading = true;
  state.error = "";
  render();
  try {
    if (catalog) {
      const c = await api("catalog?accountId=" + encodeURIComponent(aid));
      if (gen !== state.generation) return;
      state.catalog = c;
    }
    if (region) {
      const items = await api(
        "instances?" + new URLSearchParams({ accountId: aid, region }),
      );
      if (gen !== state.generation) return;
      state.instances = items;
    }
  } catch (e) {
    if (gen === state.generation) state.error = e.message;
  } finally {
    if (gen === state.generation) {
      state.loading = false;
      render();
    }
  }
}
function openDialog(title, body) {
  dialog.innerHTML = `<header class="dialog-head"><h2 id="dialog-title">${esc(title)}</h2><button class="quiet" type="button" data-close aria-label="关闭">✕</button></header><div class="dialog-body">${body}</div>`;
  dialog
    .querySelectorAll("[data-close]")
    .forEach((b) => (b.onclick = () => dialog.close()));
  if (!dialog.open) dialog.showModal();
}
const credentialFields = {
  aws: [
    ["accessKeyId", "Access Key ID"],
    ["secretAccessKey", "Secret Access Key"],
    ["sessionToken", "Session Token（临时凭据可选）"],
    ["defaultRegion", "验证地区（可选，默认 us-east-1）"],
  ],
  azure: [
    ["tenantId", "租户 ID"],
    ["clientId", "应用 Client ID"],
    ["clientSecret", "Client Secret"],
    ["subscriptionId", "订阅 ID"],
    ["resourceGroup", "资源组名称"],
  ],
  linode: [["token", "Personal Access Token"]],
  digitalocean: [["token", "Personal Access Token"]],
};
function accountDialog(acc) {
  let provider = acc?.provider || "aws";
  openDialog(
    acc ? "编辑云账号" : "接入云账号",
    `<form id="account-form"><div class="error-box" role="alert"></div><label class="field">账号备注<input name="name" maxlength="100" value="${esc(acc?.name || "")}" placeholder="例如：个人开发账号" required></label><label class="field">云厂商<select name="provider" ${acc ? "disabled" : ""}>${Object.entries(
      providers,
    )
      .map(
        ([v, n]) =>
          `<option value="${v}" ${v === provider ? "selected" : ""}>${n}</option>`,
      )
      .join(
        "",
      )}</select></label>${acc ? '<label class="check"><input type="checkbox" name="rotate">更新凭据（留空则保留现有凭据）</label>' : ""}<div id="credentials"></div><p class="help">凭据只用于你授权的云资源操作。保存时验证可读权限；创建、删除还需要对应写权限。Azure 限定到填写的资源组。</p><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit">验证并保存</button></div></form>`,
  );
  const form = dialog.querySelector("form");
  const fields = () => {
    const show = !acc || form.rotate.checked;
    form.querySelector("#credentials").innerHTML = show
      ? credentialFields[provider]
          .map(
            ([k, l]) =>
              `<label class="field">${l}<input name="${k}" type="${["token", "clientSecret", "secretAccessKey", "sessionToken"].includes(k) ? "password" : "text"}" autocomplete="off" maxlength="8192" ${["sessionToken", "defaultRegion"].includes(k) ? "" : "required"}></label>`,
          )
          .join("")
      : "";
  };
  fields();
  form.provider.onchange = () => {
    provider = form.provider.value;
    fields();
  };
  if (acc) form.rotate.onchange = fields;
  form.onsubmit = (e) => {
    e.preventDefault();
    formAction(form, async () => {
      const credentials = {};
      if (!acc || form.rotate.checked) {
        for (const [k] of credentialFields[provider])
          if (form.elements[k].value) credentials[k] = form.elements[k].value;
      }
      await api("accounts" + (acc ? "/" + acc.id : ""), {
        method: acc ? "PUT" : "POST",
        body: {
          name: form.elements.name.value,
          ...(!acc ? { provider } : {}),
          ...(!acc || form.rotate.checked ? { credentials } : {}),
        },
      });
      form.reset();
      dialog.close();
      await loadAccounts();
      state.tab = "accounts";
      shell();
      toast("云账号已保存");
    });
  };
}
function renderAccounts(el) {
  el.innerHTML =
    heading(
      "云账号",
      "管理接入信息与访问凭据。",
      `<button class="primary" id="add-account">＋ 接入云账号</button>`,
    ) +
    (state.accounts.length
      ? `<div class="account-list">${state.accounts.map((a, n) => `<article class="account-row"><div class="provider-icon">${esc(providers[a.provider].slice(0, 2))}</div><div class="account-info"><h3>${esc(a.name)}</h3><div class="secondary">${providers[a.provider]} · 接入于 ${date(a.created)}</div></div><div class="row-actions"><button data-view="${n}">查看服务器</button><button data-edit="${n}">编辑</button><button class="danger" data-remove="${n}">移除</button></div></article>`).join("")}</div>`
      : '<section class="panel"><div class="empty"><div class="empty-icon">◈</div><h2>还没有接入云账号</h2><p>选择云厂商并填写凭据，保存后即可查询资源。</p></div></section>') +
    '<p class="footer-note">移除账号只移除本地访问凭据，不会删除云端服务器。操作记录仍会保留。</p>';
  el.querySelector("#add-account").onclick = () => accountDialog();
  el.querySelectorAll("[data-edit]").forEach(
    (b) => (b.onclick = () => accountDialog(state.accounts[+b.dataset.edit])),
  );
  el.querySelectorAll("[data-view]").forEach(
    (b) =>
      (b.onclick = () => {
        state.accountId = state.accounts[+b.dataset.view].id;
        state.region = "";
        state.catalog = null;
        state.instances = [];
        state.tab = "instances";
        shell();
        refresh(true);
      }),
  );
  el.querySelectorAll("[data-remove]").forEach(
    (b) =>
      (b.onclick = () => {
        const a = state.accounts[+b.dataset.remove];
        openDialog(
          "移除云账号",
          `<form><p class="sub">云端资源不会被删除。本地凭据将移除，历史任务保留。</p><div class="error-box" role="alert"></div><label class="field">输入账号备注「${esc(a.name)}」确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="danger" type="submit">移除账号</button></div></form>`,
        );
        const f = dialog.querySelector("form");
        f.onsubmit = (e) => {
          e.preventDefault();
          formAction(f, async () => {
            await api("accounts/" + a.id, {
              method: "DELETE",
              body: { confirmName: f.confirmName.value },
            });
            dialog.close();
            await loadAccounts();
            render();
          });
        };
      }),
  );
}
async function createDialog() {
  const intentId = crypto.randomUUID();
  if (!account()) return;
  const aid = state.accountId,
    provider = account().provider;
  let catalog = state.catalog,
    seq = 0;
  openDialog(
    "创建服务器",
    `<form id="create-form"><div class="error-box" role="alert"></div><p class="sub">${esc(providers[provider])} · ${esc(account().name)}</p><div class="section-title"><span class="step">1</span>地区与配置</div><label class="field">服务器名称<input name="name" pattern="[A-Za-z0-9-]{1,63}" maxlength="63" required placeholder="例如：dev-server-01"></label><div class="grid2"><label class="field">地区<select name="region" required>${options(catalog?.regions, state.region, "正在加载地区…")}</select></label><label class="field">规格<select name="plan" required disabled><option value="">先选择地区</option></select></label></div><label class="field">搜索系统镜像<input name="imageSearch" placeholder="Ubuntu、Debian、Windows Server…" type="search"></label><label class="field">系统镜像<select name="image" required disabled><option value="">先选择地区和规格</option></select><p class="help">优先展示上游维护中的版本。维护期来源于官方发行数据；无法核实时标记未确认。</p></label><label class="check"><input name="showOlder" type="checkbox">显示维护结束或尚未发布的镜像</label><div id="network-fields"></div><div class="section-title"><span class="step">2</span>登录与启动</div><div id="access-fields"></div><details><summary>更多启动选项</summary>${["aws", "digitalocean"].includes(provider) ? '<label class="field">初始化脚本（可选）<textarea name="userData" maxlength="32768" placeholder="#!/bin/sh"></textarea></label>' : ""}${["aws", "digitalocean"].includes(provider) ? '<label class="check"><input name="ipv6" type="checkbox">分配 IPv6（需要地区及网络支持）</label>' : ""}${["linode", "digitalocean"].includes(provider) ? '<label class="check"><input name="backups" type="checkbox">启用云厂商自动备份（可能额外收费）</label>' : ""}${provider === "azure" ? '<label class="field">系统盘容量 GB（留空使用镜像默认值）<input name="disk" type="number" min="1" max="4095"></label>' : ""}</details><div class="section-title"><span class="step">3</span>确认配置</div><div class="notice">创建会产生云服务费用。提交后可在操作任务中查看进度。${provider === "azure" ? "将自动创建公网 IP 和网卡，资源名称附加任务后缀。所选子网需配置合适的安全规则。" : ""}</div><label class="field">再次输入服务器名称确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit" disabled>确认创建</button></div></form>`,
  );
  const f = dialog.querySelector("form");
  function access() {
    f.querySelector("#access-fields").innerHTML =
      `${["aws", "digitalocean", "linode"].includes(provider) ? `<label class="field">${provider === "aws" ? "SSH 密钥对" : "账号 SSH 密钥"}${provider === "linode" ? "（可选）" : ""}<select name="key" ${provider === "linode" ? "" : "required"}>${options(catalog?.keys, "", "选择已有密钥")}</select></label>` : ""}${["azure", "linode"].includes(provider) ? `${provider === "azure" ? '<label class="field">管理来源 IPv4 CIDR<input name="managementCidr" placeholder="你的公网 IP/32" required><p class="help">只允许这个可信地址范围访问 SSH 或 RDP。请在公网 IP 变化后到 Azure 控制台更新规则。</p></label><label class="field">管理员用户名<input name="username" value="narbe" pattern="[a-z0-9_-]{1,20}" required></label>' : ""}<label class="field">SSH 公钥${provider === "azure" ? "（Linux 必填）" : "（可选）"}<textarea name="sshPublicKey" placeholder="ssh-ed25519 AAAA…" maxlength="16384"></textarea></label><label class="field">${provider === "linode" ? "root 密码（使用 SSH 密钥可留空）" : "管理员密码（Windows 必填）"}<input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="128"><p class="help">避免重复字符、连续数字和常见口令，建议使用密码管理器生成独立密码。云端还会检查密码强度。</p></label>` : ""}${provider === "aws" ? '<p class="help">Windows 请使用 RSA 密钥对，初始密码在 AWS 控制台解密查看。这里不会接收你的私钥。</p>' : ""}`;
  }
  function images() {
    const plan = catalog?.plans?.find((p) => p.id === f.plan.value),
      q = f.imageSearch.value.toLowerCase(),
      previous = f.image.value;
    const ims = (catalog?.images || [])
      .filter(
        (i) =>
          !i.deprecated &&
          (f.showOlder.checked ||
            !["ended", "unreleased"].includes(i.maintenance)) &&
          (!i.architecture ||
            !plan?.architecture ||
            i.architecture === plan.architecture) &&
          (!q || i.name.toLowerCase().includes(q) || i.family.includes(q)),
      )
      .sort(
        (a, b) =>
          ({
            standard: 0,
            lts: 1,
            extended: 2,
            unknown: 3,
            ended: 4,
            unreleased: 5,
          })[a.maintenance || "unknown"] -
            {
              standard: 0,
              lts: 1,
              extended: 2,
              unknown: 3,
              ended: 4,
              unreleased: 5,
            }[b.maintenance || "unknown"] ||
          a.family.localeCompare(b.family) ||
          b.name.localeCompare(a.name, undefined, { numeric: true }),
      );
    f.image.innerHTML = options(ims, previous, "选择可用系统镜像");
    f.image.disabled = !ims.length;
    f.querySelector("button[type=submit]").disabled =
      !catalog?.plans?.length || !ims.length;
  }
  async function load() {
    const current = ++seq;
    f.querySelector("button[type=submit]").disabled = true;
    f.plan.disabled = true;
    f.image.disabled = true;
    f.querySelector(".error-box").textContent = "";
    try {
      catalog = await api(
        "catalog?" +
          new URLSearchParams({ accountId: aid, region: f.region.value }),
      );
      if (current !== seq || !dialog.open) return;
      f.plan.innerHTML = options(catalog.plans, "", "选择规格");
      f.plan.disabled = !catalog.plans.length;
      f.querySelector("#network-fields").innerHTML = ["aws", "azure"].includes(
        provider,
      )
        ? `<label class="field">${provider === "aws" ? "子网" : "已有子网"}<select name="network" required>${options(catalog.networks)}</select></label>${provider === "aws" ? `<label class="field">安全组<select name="group" required>${options(catalog.groups)}</select><p class="help">需要与子网属于同一 VPC；请只向可信地址开放管理端口。</p></label>` : '<p class="help">使用当前资源组内已有 VNet 子网。不会修改或删除已有网络。</p>'}`
        : "";
      access();
      images();
    } catch (e) {
      if (current === seq)
        f.querySelector(".error-box").textContent = e.message;
    }
  }
  access();
  f.region.onchange = load;
  f.plan.onchange = images;
  f.imageSearch.oninput = images;
  f.showOlder.onchange = images;
  f.onsubmit = (e) => {
    e.preventDefault();
    formAction(f, async () => {
      const data = Object.fromEntries(new FormData(f));
      const im = catalog.images.find((i) => i.id === data.image);
      const spec = {
        name: data.name,
        region: data.region,
        plan: data.plan,
        image: data.image,
        architecture: im?.architecture || "",
        network: data.network || "",
        group: data.group || "",
        key: data.key || "",
        sshPublicKey: data.sshPublicKey || "",
        password: data.password || "",
        username: data.username || "",
        managementCidr: data.managementCidr || "",
        userData: data.userData || "",
        ipv6: !!data.ipv6,
        backups: !!data.backups,
        disk: Number(data.disk) || 0,
      };
      const body = {
        id: intentId,
        accountId: aid,
        action: "create",
        region: spec.region,
        target: "",
        name: spec.name,
        spec,
        confirmation: "",
        confirmName: data.confirmName,
      };
      await submit(body);
      f.reset();
      dialog.close();
    });
  };
  if (!catalog?.regions) {
    try {
      catalog = await api("catalog?accountId=" + encodeURIComponent(aid));
      if (!dialog.open) return;
      f.region.innerHTML = options(catalog.regions, state.region);
    } catch (e) {
      f.querySelector(".error-box").textContent = e.message;
    }
  }
  if (f.region.value) await load();
}
async function submit(body) {
  const c = await api("confirm", { method: "POST", body });
  body.confirmation = c.confirmation;
  body.accountRevision = c.accountRevision;
  await api("jobs", { method: "POST", body });
  state.tab = "jobs";
  await updateJobs();
  shell();
  toast("操作已提交，请查看任务进度");
}
function actionDialog(i, action) {
  const intentId = crypto.randomUUID();
  if (!action) return;
  const provider = account().provider;
  let warning =
    action === "delete"
      ? "删除无法撤销，请先确认已备份重要数据。"
      : action === "stop"
        ? "关机可能中断业务。磁盘和保留 IP 仍可能计费。"
        : "操作可能短暂中断业务。";
  if (action === "delete" && provider === "azure")
    warning +=
      " Azure 仅删除虚拟机，磁盘、网卡、网络安全组和公网 IP 可能仍然存在；磁盘和公网 IP 等资源可能继续计费。删除成功不代表全部费用已停止。";
  if (action === "delete" && provider === "linode")
    warning += " Linode 会同时删除系统磁盘和实例备份。";
  if (action === "poweroff")
    warning += " 此操作保留 Azure 计算分配，计算费用仍会继续。";
  openDialog(
    labels[action],
    `<form><div class="error-box" role="alert"></div><div class="notice">${esc(warning)}</div><dl class="summary"><dt>服务器</dt><dd>${esc(i.name)}</dd><dt>地区</dt><dd>${esc(i.region)}</dd><dt>资源标识</dt><dd class="mono">${esc(i.id)}</dd></dl><label class="field">输入服务器名称「${esc(i.name)}」确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="${action === "delete" ? "danger" : "primary"}" type="submit">确认${labels[action]}</button></div></form>`,
  );
  const f = dialog.querySelector("form");
  f.onsubmit = (e) => {
    e.preventDefault();
    formAction(f, async () => {
      await submit({
        id: intentId,
        accountId: state.accountId,
        action,
        region: i.region,
        target: i.id,
        name: i.name,
        spec: {},
        confirmation: "",
        confirmName: f.confirmName.value,
      });
      dialog.close();
    });
  };
}
async function updateJobs() {
  try {
    state.jobs = await api("jobs");
    if (state.tab === "jobs") render();
  } catch (e) {
    if (state.csrf) toast(e.message);
  }
}
function deletionLedgerDetails(job) {
  if (job.provider !== "azure" || job.action !== "delete") return "";
  const ledger = job.result?.ledger;
  const warning =
    '<p class="notice">磁盘和公网 IP 等关联资源可能继续计费。虚拟机删除完成不代表全部费用已停止，请到 Azure 控制台逐项核实。</p>';
  if (ledger?.status !== "linked")
    return (
      warning +
      '<details class="deletion-ledger"><summary>关联资源与费用检查</summary><p>关联资源未知，需要到 Azure 控制台核实。请检查此虚拟机原有的磁盘、网卡、网络安全组及公网 IP；Narbe 不会自动删除这些资源。</p></details>'
    );
  const kinds = {
    disk: "系统盘",
    nic: "网卡",
    nsg: "网络安全组",
    "public-ip": "公网 IP",
  };
  return (
    warning +
    `<details class="deletion-ledger"><summary>关联资源与费用检查</summary><p>以下来自该虚拟机的 Narbe 创建账本，当前存在及收费状态尚未核实。请复制资源 ID 到 Azure 控制台逐项检查；Narbe 不会自动删除这些资源。</p><dl class="summary">${ledger.resources.map((resource) => `<dt>${esc(kinds[resource.kind] || resource.kind)}</dt><dd><span class="mono">${esc(resource.id)}</span><br>状态：存在及费用未核实</dd>`).join("")}</dl><p class="mono">来源任务：${esc(ledger.sourceJobId)}</p></details>`
  );
}
function renderJobs(el) {
  el.innerHTML =
    heading(
      "操作任务",
      "查看进度与结果。结果未知的操作不会自动重新执行。",
      `<button id="refresh-jobs">↻ 刷新</button>`,
    ) +
    `<section class="panel">${state.jobs.length ? state.jobs.map((j) => `<article class="task-row"><div class="task-top"><div><h3>${esc(labels[j.action] || j.action)} · ${esc(j.name)}</h3><div class="secondary">${esc(state.accounts.find((a) => a.id === j.accountId)?.name || "已移除账号")} · ${esc(j.region)} · ${date(j.created)}</div></div>${badge(j.state === "running" ? "provisioning" : j.state)}</div><p>${esc(j.progress)}${j.error ? " · " + esc(j.error) : ""}</p>${deletionLedgerDetails(j)}<details><summary>资源与任务标识</summary><pre>${esc("任务：" + j.id + "\n资源：" + (j.result?.resourceId || j.target || "尚未取得") + (j.result?.owned?.length ? "\n关联资源（可能继续计费）：\n" + j.result.owned.join("\n") : ""))}</pre></details>${j.state === "unknown" ? `<p><button data-reconcile="${esc(j.id)}">只读核实云端状态</button> <button data-resolve="${esc(j.id)}" data-name="${esc(j.name)}">已在云控制台核实</button></p>` : ""}${j.state === "queued" ? `<p><button data-cancel="${esc(j.id)}">取消等待中的操作</button></p>` : ""}</article>`).join("") : '<div class="empty"><div class="empty-icon">◷</div><h2>还没有操作任务</h2><p>创建或管理服务器后，操作进度会显示在这里。</p></div>'}</section><p class="footer-note">最多显示最近 200 条记录。未派发任务等待或校验超过 5 分钟、应用重启时会取消，需重新检查并确认。已提交操作结果未知时，请先核实云端，不要直接重复创建。</p>`;
  el.querySelector("#refresh-jobs").onclick = updateJobs;
  for (const [attr, action] of [
    ["reconcile", "reconcile"],
    ["cancel", "cancel"],
  ])
    el.querySelectorAll(`[data-${attr}]`).forEach(
      (b) =>
        (b.onclick = async () => {
          b.disabled = true;
          try {
            await api("jobs/" + b.dataset[attr] + "/" + action, {
              method: "POST",
              body: {},
            });
            await updateJobs();
            toast(action === "reconcile" ? "已核实目标状态" : "任务已取消");
          } catch (e) {
            toast(e.message);
            b.disabled = false;
          }
        }),
    );
}
function resolveDialog(id, name) {
  openDialog(
    "记录人工核实",
    `<form><div class="error-box" role="alert"></div><div class="notice">请先在云控制台检查实例及关联资源。未找到资源不保证请求未发生；不要立即重复创建。此操作只解除本地阻塞，不会重试或删除云资源。</div><label class="field">输入服务器名称「${esc(name)}」<input name="confirmName" required></label><label class="field">管理员密码<input name="password" type="password" autocomplete="current-password" required></label><label class="check"><input name="checked" type="checkbox" required>我已在云控制台核实全部关联资源，并了解重复创建的费用风险</label><div class="dialog-actions"><button type="button" data-close>取消</button><button type="submit" class="primary">记录核实并解除阻塞</button></div></form>`,
  );
  const f = dialog.querySelector("form");
  f.onsubmit = (e) => {
    e.preventDefault();
    formAction(f, async () => {
      await api("jobs/" + id + "/resolve", {
        method: "POST",
        body: {
          confirmName: f.confirmName.value,
          password: f.password.value,
          checked: f.checked.checked,
        },
      });
      f.reset();
      dialog.close();
      await updateJobs();
    });
  };
}
document.addEventListener("click", (e) => {
  const b = e.target.closest("[data-resolve]");
  if (b) resolveDialog(b.dataset.resolve, b.dataset.name);
});

function renderSettings(el) {
  el.innerHTML =
    heading("设置", "管理工作台的登录密码。") +
    `<form class="panel settings" id="password-form"><h2>更改管理员密码</h2><p class="sub">更改后所有登录会话都会失效，需要重新登录。</p><div class="error-box" role="alert"></div><label class="field">当前密码<input name="current" type="password" autocomplete="current-password" required></label><label class="field">新密码<input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="256" required><p class="help">至少 12 字符，建议使用密码管理器生成独立密码。</p></label><label class="field">再次输入新密码<input name="confirm" type="password" autocomplete="new-password" required></label><button class="primary" type="submit">更新密码</button></form>`;
  const f = el.querySelector("form");
  f.onsubmit = (e) => {
    e.preventDefault();
    formAction(f, async () => {
      if (f.password.value !== f.confirm.value)
        throw Error("两次输入的新密码不一致");
      await api("password", {
        method: "POST",
        body: { current: f.current.value, password: f.password.value },
      });
      f.reset();
      state.csrf = "";
      state.generation++;
      login();
      toast("密码已更新，请重新登录");
    });
  };
}
try {
  const s = await api("session");
  state.csrf = s.csrf;
  await loadAccounts();
  await updateJobs();
  shell();
  if (state.accountId) refresh(true);
} catch {
  login();
}
setInterval(() => {
  if (state.csrf && !document.hidden) updateJobs();
}, 5000);
