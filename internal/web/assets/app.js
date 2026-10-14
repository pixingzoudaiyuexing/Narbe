import {
  initLinode,
  linodeDetails,
  linodeAccount,
  firewallFields,
  readFirewallSelection,
  loadFirewallOptions,
  groupedRegions,
} from "./linode-ui.js";
import {
  configLabel,
  priceLabel,
  stateGroup,
  regionLabel,
  inventoryMessage,
  serverStateLabel,
} from "./format.js";
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
  instancesLoaded: false,
  jobs: [],
  error: "",
  loading: false,
  generation: 0,
  search: "",
  statusFilter: "",
  regionSearch: "",
  listPage: 1,
};
const providers = {
  aws: "AWS",
  azure: "Azure",
  linode: "Linode",
  digitalocean: "DigitalOcean",
};
const labels = {
  rebuild: "重装系统",
  resize: "调整配置",
  "password-reset": "重置root密码",
  clone: "克隆新服务器",
  migrate: "迁移数据中心",
  "firewall-create": "创建云防火墙",
  "firewall-rules": "修改防火墙规则",
  "firewall-status": "切换防火墙状态",
  "firewall-delete": "删除云防火墙",
  "firewall-bind": "调整服务器防火墙",
  create: "创建服务器",
  start: "开机",
  stop: "关机",
  poweroff: "仅关机（计算仍计费）",
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
const inventories = new Map();
const inventoryFlights = new Map();
const inventoryQueue = [];
let inventoryActive = 0;
const currentInventory = () => inventories.get(state.accountId)?.snapshot;
function discardOtherDetails(selected) {
  for (const [aid, record] of inventories) {
    if (aid !== selected && record.snapshot && !record.snapshot.summaryOnly) {
      inventories.set(aid, {
        ...record,
        snapshot: { ...record.snapshot, items: [], summaryOnly: true },
      });
    }
  }
}
function applyInventory(aid, revision, snapshot) {
  const acc = state.accounts.find((a) => a.id === aid);
  if (
    !acc ||
    acc.revision !== revision ||
    snapshot.accountId !== aid ||
    snapshot.accountRevision !== revision
  )
    return;
  const view =
    aid === state.accountId
      ? snapshot
      : { ...snapshot, items: [], summaryOnly: true };
  inventories.set(aid, { revision, snapshot: view, error: "" });
  if (aid === state.accountId) {
    state.instances = snapshot.items || [];
    state.instancesLoaded = snapshot.phase === "ready" && !snapshot.summaryOnly;
    state.loading = snapshot.phase !== "ready" || snapshot.summaryOnly;
    state.catalog = { regions: snapshot.regions || [] };
  }
  render();
}
function queueInventory(
  aid,
  { force = false, retry = "", summary = false } = {},
) {
  const acc = state.accounts.find((a) => a.id === aid);
  if (!acc) return Promise.resolve();
  const existing = inventoryFlights.get(aid);
  if (existing)
    return existing.promise.then((v) =>
      !summary && existing.summary
        ? queueInventory(aid, { summary: false })
        : v,
    );
  const controller = new AbortController();
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  const flight = { promise, controller, summary, revision: acc.revision };
  inventoryFlights.set(aid, flight);
  inventoryQueue.push(async () => {
    try {
      let first = true;
      for (;;) {
        if (controller.signal.aborted || !state.csrf) return;
        const query = new URLSearchParams({
          accountId: aid,
          ...(summary ? { summary: "1" } : {}),
          ...(first && force ? { refresh: "1" } : {}),
          ...(first && retry ? { retry } : {}),
        });
        const snapshot = await api("inventory?" + query, {
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        first = false;
        // A summary and a full list must never be merged across snapshots.
        snapshot.summaryOnly = summary;
        applyInventory(aid, acc.revision, snapshot);
        if (snapshot.phase === "ready") return snapshot;
        await new Promise((yes) => setTimeout(yes, 1000));
      }
    } catch (e) {
      if (
        !controller.signal.aborted &&
        state.accounts.some((a) => a.id === aid && a.revision === acc.revision)
      ) {
        const old = inventories.get(aid);
        inventories.set(aid, {
          revision: acc.revision,
          snapshot: old?.snapshot,
          error: e.message,
        });
        if (aid === state.accountId) {
          state.loading = false;
          state.error = e.message;
        }
        render();
      }
      throw e;
    } finally {
      if (inventoryFlights.get(aid) === flight) inventoryFlights.delete(aid);
    }
  });
  const work = inventoryQueue.pop();
  inventoryQueue.push(async () => {
    try {
      resolve(await work());
    } catch (e) {
      reject(e);
    }
  });
  drainInventoryQueue();
  return promise;
}
function drainInventoryQueue() {
  while (inventoryActive < 2 && inventoryQueue.length) {
    inventoryActive++;
    inventoryQueue
      .shift()()
      .finally(() => {
        inventoryActive--;
        drainInventoryQueue();
      });
  }
}
function warmInventories() {
  const ordered = [...state.accounts].sort(
    (a, b) =>
      Number(b.id === state.accountId) - Number(a.id === state.accountId),
  );
  for (const a of ordered)
    if (!inventories.has(a.id))
      queueInventory(a.id, { summary: a.id !== state.accountId }).catch(
        () => {},
      );
}
function cancelInventories() {
  for (const flight of inventoryFlights.values()) flight.controller.abort();
  inventoryFlights.clear();
  inventories.clear();
}
function regionChoices(regions, query = "", selected = "") {
  const q = query.toLocaleLowerCase();
  return (regions || []).filter(
    (r) =>
      r.id === selected ||
      !q ||
      (r.name + " " + r.id).toLocaleLowerCase().includes(q),
  );
}
function snapshotInfo(aid) {
  const record = inventories.get(aid),
    v = record?.snapshot;
  if (record?.error)
    return `<p class="error-box">${esc(record.error)}；当前总数未确认。</p>${v?.updatedAt ? `<p class="help">上次查询结束于 ${date(v.updatedAt)}；保留的列表仅供参考，请重试刷新。</p>` : ""}`;
  return `<p class="inventory-total">${esc(inventoryMessage(v))}</p>${v ? `${v.phase === "ready" || v.stats.total > 0 ? `<p class="help">${v.complete ? "" : "已确认部分："}运行中 ${v.stats.running} 台 · 已关机 ${v.stats.stopped} 台 · 其他状态 ${v.stats.other} 台</p>` : '<p class="help">运行与关机数量正在查询…</p>'}<p class="help">${esc(v.scope)} · ${v.updatedAt ? "查询完成于 " + date(v.updatedAt) : "查询尚未完成"} · ${v.complete ? "列表查询完整" : "总数未确认"}</p><p class="help">${v.startedAt ? "采集开始于 " + date(v.startedAt) + "；" : ""}各地区为分批查询，状态并非同一时刻快照。手动刷新至少间隔15秒。</p>` : ""}`;
}
function intentID() {
  if (!globalThis.crypto?.getRandomValues) {
    toast("浏览器功能不兼容，暂时无法提交操作。请升级浏览器后重试");
    return "";
  }
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  // getRandomValues remains available on HTTP. Preserve UUID v4 entropy and
  // the immutable per-dialog ID; never fall back to Math.random or timestamps.
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join(
    "",
  );
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
const date = (t) =>
  new Date(t * 1000).toLocaleString("zh-CN", { hour12: false });
function badge(s, server = false, provider = "") {
  let cls = (server
    ? ["running", "active", "on"]
    : ["running", "active", "on", "succeeded"]
  ).includes(s)
    ? "good"
    : ["failed"].includes(s)
      ? "bad"
      : ["unknown", "queued"].includes(s)
        ? "warn"
        : "";
  return `<span class="status ${cls}">${esc(server ? serverStateLabel(s, provider) : statuses[s] || s)}</span>`;
}
function toast(s) {
  document.querySelector("#toast").textContent = s;
  setTimeout(() => (document.querySelector("#toast").textContent = ""), 4500);
}
async function api(path, { method = "GET", body, signal } = {}) {
  let res;
  try {
    res = await fetch("/api/" + path, {
      method,
      signal,
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
      if (state.accountId) await refresh();
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
  for (const [aid, flight] of inventoryFlights) {
    if (
      !state.accounts.some(
        (a) => a.id === aid && a.revision === flight.revision,
      )
    ) {
      flight.controller.abort();
      inventoryFlights.delete(aid);
    }
  }
  for (const [aid, record] of inventories) {
    if (
      !state.accounts.some(
        (a) => a.id === aid && a.revision === record.revision,
      )
    ) {
      inventoryFlights.get(aid)?.controller.abort();
      inventories.delete(aid);
    }
  }
  if (!state.accounts.some((a) => a.id === state.accountId)) {
    state.accountId = state.accounts[0]?.id || "";
    state.region = "";
    state.catalog = null;
    state.instances = [];
    state.instancesLoaded = false;
  }
  warmInventories();
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
        if (state.tab === "instances" && state.accountId) refresh();
      }),
  );
  document.querySelector("#logout").onclick = async () => {
    try {
      await api("logout", { method: "POST" });
      state.csrf = "";
      state.generation++;
      cancelInventories();
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
  const focus = document.activeElement;
  const focusedID = content.contains(focus) ? focus.id : "";
  const cursor = focus?.selectionStart;
  if (state.tab === "accounts") renderAccounts(content);
  else if (state.tab === "jobs") renderJobs(content);
  else if (state.tab === "settings") renderSettings(content);
  else renderInstances(content);
  if (focusedID) {
    const next = document.getElementById(focusedID);
    next?.focus();
    if (typeof cursor === "number" && next?.setSelectionRange)
      next.setSelectionRange(cursor, cursor);
  }
}
function renderInstances(el) {
  const acc = account(),
    record = inventories.get(state.accountId),
    v = record?.snapshot;
  const q = state.search.toLocaleLowerCase();
  const filtered = (v?.items || []).filter(
    (i) =>
      (!state.region || i.region === state.region) &&
      (!state.statusFilter || stateGroup(i.state) === state.statusFilter) &&
      (!q ||
        (i.name + " " + i.id + " " + (i.ips || []).join(" "))
          .toLocaleLowerCase()
          .includes(q)),
  );
  const loading =
    acc && (!v || v.phase !== "ready" || v.summaryOnly) && !record?.error;
  const pages = Math.max(1, Math.ceil(filtered.length / 50));
  state.listPage = Math.min(state.listPage, pages);
  const items = filtered.slice((state.listPage - 1) * 50, state.listPage * 50);
  const regions = regionChoices(v?.regions, state.regionSearch, state.region);
  const table = `<p class="scroll-hint">← 左右滑动表格查看配置与操作 →</p><div class="table-wrap"><table><thead><tr><th>服务器 / 账号</th><th>状态</th><th>公网 IPv4 / IPv6</th><th>位置 / 配置</th><th>管理</th></tr></thead><tbody>${items.map((i, n) => `<tr><td><button class="quiet name" data-details="${n}">${esc(i.name)}</button><div class="secondary">${esc(providers[acc.provider])} · ${esc(acc.name)}</div><div class="secondary mono" title="${esc(i.id)}">${esc(i.id)}</div></td><td>${badge(i.state, true, acc.provider)}</td><td>${i.ips?.length ? i.ips.map((ip) => `<div class="mono">${esc(ip)}</div>`).join("") : '<span class="help">未分配或未确认</span>'}</td><td><div>${esc(regionLabel(v.regions, i.region, i.regionName))}</div><div class="config-text">${esc(configLabel(i, acc.provider))}</div><div class="secondary mono">${esc(i.plan || "型号未确认")}</div></td><td><div class="row-actions"><select aria-label="${esc(i.name)} 的操作" data-action-select="${n}">${i.actions.map((a) => `<option value="${a}">${esc(labels[a])}</option>`).join("")}</select><button data-action="${n}">执行</button></div></td></tr>`).join("")}</tbody></table></div>`;
  let body;
  if (!state.accounts.length)
    body =
      '<div class="empty"><div class="empty-icon">◈</div><h2>先接入你的第一个云账号</h2><p>连接 AWS、Azure、Linode 或 DigitalOcean，<br>即可在这里查看和管理服务器。</p><button class="primary" id="connect">接入云账号 →</button></div>';
  else if (items.length) body = table;
  else if (loading)
    body =
      '<div class="loading" role="status">正在查询云端资源，数量尚未确认…</div>';
  else if (record?.error || !v?.complete)
    body =
      '<div class="empty"><h2>服务器总数暂未确认</h2><p>部分资源可能尚未查询到，请重试失败范围。</p></div>';
  else if (v.stats.total > 0)
    body =
      '<div class="empty"><h2>没有符合筛选条件的服务器</h2><p>清除地区、状态或搜索条件即可查看全部结果。</p><button id="clear-filters">清除筛选</button></div>';
  else
    body = `<div class="empty"><div class="empty-icon">▦</div><h2>${state.region ? "这个地区还没有服务器" : "当前范围还没有服务器"}</h2><p>云端完整查询确认此范围为0台。创建服务器会产生费用。</p><button class="primary" id="empty-create">创建服务器</button></div>`;
  el.innerHTML =
    heading(
      "服务器",
      "查看所有地区的资源，再按位置、状态或名称筛选。",
      `<button class="primary" id="create" ${!acc ? "disabled" : ""}>＋ 创建服务器</button>`,
    ) +
    (acc
      ? `<div class="toolbar"><label>云账号<select id="account">${options(
          state.accounts.map((a) => ({
            id: a.id,
            name: providers[a.provider] + " · " + a.name,
          })),
          state.accountId,
          "选择账号",
        )}</select></label><label>搜索地区<input id="region-search" type="search" value="${esc(state.regionSearch)}" placeholder="中文位置或地区代码"></label><label>地区<select id="region">${groupedRegions(regions, state.region, "全部地区")}</select></label><label>状态<select id="status-filter">${options(
          [
            { id: "running", name: "运行中" },
            { id: "stopped", name: "已关机" },
            { id: "other", name: "其他状态" },
          ],
          state.statusFilter,
          "全部状态",
        )}</select></label><label>搜索服务器<input id="instance-search" type="search" value="${esc(state.search)}" placeholder="服务器名称、IP 或实例 ID"></label><button id="refresh" ${loading ? "disabled" : ""}>${loading ? "正在查询…" : "↻ 刷新资源"}</button></div><section class="inventory-summary">${snapshotInfo(acc.id)}${v?.warnings?.map((m) => `<p class="help">${esc(m)}</p>`).join("") || ""}${v?.failures?.map((f, n) => `<div class="failure-row"><span>${esc(f.region === "*" ? "当前查询范围" : regionLabel(v.regions, f.region, f.regionName))}：${esc(f.message)}</span><button data-retry="${n}" ${loading ? "disabled" : ""}>重试此范围</button></div>`).join("") || ""}</section>`
      : "") +
    `<div class="error-box" role="alert">${esc(state.error)}</div>${state.jobs.some((j) => j.state === "unknown") ? '<div class="notice">有操作结果待核实。请先到「操作任务」核实云端结果。</div>' : ""}<section class="panel"><div class="panel-head"><h2>实例列表<span class="count">${loading ? "…" : v?.complete || filtered.length ? filtered.length : "—"}</span></h2><span class="help">${state.region ? esc(regionLabel(v?.regions, state.region)) : "全部地区"} · ${v ? `已发现 ${v.stats.total} 台${record?.error ? "（上次结果，当前总数未确认）" : v.complete ? "" : "，总数未确认"}` : "尚未完成查询"}</span></div>${body}${filtered.length > 50 ? `<div class="pagination"><button id="previous-page" ${state.listPage === 1 ? "disabled" : ""}>上一页</button><span>第 ${state.listPage} / ${pages} 页 · 每页最多50台</span><button id="next-page" ${state.listPage === pages ? "disabled" : ""}>下一页</button></div>` : ""}</section><p class="footer-note">关机并不一定停止计费。磁盘、网络、公网 IP 和备份仍可能收费。${acc?.provider === "azure" ? "Azure 删除仅删除VM，保留关联磁盘和网络资源；请到控制台核实费用。" : ""}</p>`;
  el.querySelector("#previous-page")?.addEventListener("click", () => {
    state.listPage--;
    render();
  });
  el.querySelector("#next-page")?.addEventListener("click", () => {
    state.listPage++;
    render();
  });
  el.querySelector("#create")?.addEventListener("click", createDialog);
  el.querySelector("#empty-create")?.addEventListener("click", createDialog);
  el.querySelector("#connect")?.addEventListener("click", () =>
    accountDialog(),
  );
  el.querySelector("#refresh")?.addEventListener("click", () => refresh(true));
  el.querySelector("#account")?.addEventListener("change", (e) => {
    state.accountId = e.target.value;
    discardOtherDetails(state.accountId);
    state.listPage = 1;
    state.region = "";
    state.catalog = null;
    state.instances = [];
    state.search = "";
    state.regionSearch = "";
    state.statusFilter = "";
    state.error = "";
    state.generation++;
    render();
    refresh();
  });
  for (const [id, key] of [
    ["region", "region"],
    ["status-filter", "statusFilter"],
  ])
    el.querySelector("#" + id)?.addEventListener("change", (e) => {
      state[key] = e.target.value;
      state.listPage = 1;
      render();
    });
  for (const [id, key] of [
    ["instance-search", "search"],
    ["region-search", "regionSearch"],
  ])
    el.querySelector("#" + id)?.addEventListener("input", (e) => {
      state[key] = e.target.value;
      state.listPage = 1;
      render();
    });
  el.querySelector("#clear-filters")?.addEventListener("click", () => {
    state.region = "";
    state.search = "";
    state.regionSearch = "";
    state.statusFilter = "";
    render();
  });
  el.querySelectorAll("[data-retry]").forEach(
    (b) =>
      (b.onclick = () => refresh(false, v.failures[+b.dataset.retry].region)),
  );
  el.querySelectorAll("[data-action]").forEach(
    (b) =>
      (b.onclick = () => {
        const n = +b.dataset.action;
        actionDialog(
          items[n],
          el.querySelector(`[data-action-select="${n}"]`).value,
        );
      }),
  );
  el.querySelectorAll("[data-details]").forEach(
    (b) =>
      (b.onclick = () => instanceDetails(items[+b.dataset.details], acc, v)),
  );
}
async function refresh(force = false, retry = "") {
  const aid = state.accountId,
    gen = ++state.generation;
  if (!aid) return;
  state.error = "";
  try {
    await queueInventory(aid, { force, retry, summary: false });
  } catch (e) {
    if (gen === state.generation) state.error = e.message;
  }
  if (gen === state.generation) render();
}
function instanceDetails(i, acc, v) {
  if (acc.provider === "linode") {
    linodeDetails(i, acc, v);
    return;
  }
  openDialog(
    "服务器详情",
    `<dl class="summary"><dt>云厂商 / 账号</dt><dd>${esc(providers[acc.provider] + " · " + acc.name)}</dd><dt>服务器</dt><dd>${esc(i.name)}</dd><dt>地区</dt><dd>${esc(regionLabel(v.regions, i.region, i.regionName))}</dd><dt>资源 ID</dt><dd class="mono">${esc(i.id)}</dd><dt>状态</dt><dd>${badge(i.state, true, acc.provider)}</dd><dt>配置</dt><dd>${esc(configLabel(i, acc.provider))}<br>${esc(i.plan || "型号未确认")}</dd><dt>系统</dt><dd>${esc(i.image || "系统版本未确认")}</dd><dt>公网 IP</dt><dd>${esc(i.ips?.join(" / ") || "未分配或未确认")}${i.metadataIncomplete ? "（部分网络信息未确认，请检查读取权限）" : ""}</dd><dt>广告价格</dt><dd>${esc(priceLabel(i.pricing))}</dd><dt>查询范围</dt><dd>${esc(v.scope)}</dd></dl><p class="notice">配置以云厂商查询结果为准，未知信息不会视为0或免费。独立磁盘、公网IP、备份、流量及系统授权可能额外计费。管理操作提交前会重新核实资源身份。</p>`,
  );
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
      const savedName = form.elements.name.value;
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
      if (provider === "linode") {
        const saved = state.accounts.find(
          (a) =>
            a.id ===
            (acc?.id || state.accounts.find((a) => a.name === savedName)?.id),
        );
        if (saved) showLinodePermissions(saved);
      }
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
      ? `<div class="account-list">${state.accounts.map((a, n) => `<article class="account-row"><div class="provider-icon">${esc(providers[a.provider].slice(0, 2))}</div><div class="account-info"><h3>${esc(a.name)}</h3><div class="secondary">${providers[a.provider]} · 接入于 ${date(a.created)}</div>${snapshotInfo(a.id)}</div><div class="row-actions"><button data-summary-refresh="${n}">刷新统计</button><button data-view="${n}">查看服务器</button>${a.provider === "linode" ? `<button data-billing="${n}">账单</button><button data-firewalls="${n}">云防火墙</button><button data-permissions="${n}">权限</button>` : ""}<button data-edit="${n}">编辑</button><button class="danger" data-remove="${n}">移除</button></div></article>`).join("")}</div>`
      : '<section class="panel"><div class="empty"><div class="empty-icon">◈</div><h2>还没有接入云账号</h2><p>选择云厂商并填写凭据，保存后即可查询资源。</p></div></section>') +
    '<p class="footer-note">移除账号只移除本地访问凭据，不会删除云端服务器。操作记录仍会保留。</p>';
  el.querySelector("#add-account").onclick = () => accountDialog();
  el.querySelectorAll("[data-summary-refresh]").forEach(
    (b) =>
      (b.onclick = () =>
        queueInventory(state.accounts[+b.dataset.summaryRefresh].id, {
          force: true,
          summary: true,
        }).catch(() => {})),
  );
  el.querySelectorAll("[data-billing]").forEach(
    (b) =>
      (b.onclick = () =>
        linodeAccount(state.accounts[+b.dataset.billing], "billing")),
  );
  el.querySelectorAll("[data-firewalls]").forEach(
    (b) =>
      (b.onclick = () =>
        linodeAccount(state.accounts[+b.dataset.firewalls], "firewalls")),
  );
  el.querySelectorAll("[data-permissions]").forEach(
    (b) =>
      (b.onclick = () =>
        showLinodePermissions(state.accounts[+b.dataset.permissions])),
  );
  el.querySelectorAll("[data-edit]").forEach(
    (b) => (b.onclick = () => accountDialog(state.accounts[+b.dataset.edit])),
  );
  el.querySelectorAll("[data-view]").forEach(
    (b) =>
      (b.onclick = () => {
        state.accountId = state.accounts[+b.dataset.view].id;
        discardOtherDetails(state.accountId);
        state.region = "";
        state.search = "";
        state.statusFilter = "";
        state.regionSearch = "";
        state.catalog = null;
        state.instances = [];
        state.instancesLoaded = false;
        state.tab = "instances";
        shell();
        refresh();
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
  const intentId = intentID();
  if (!intentId || !account()) return;
  const aid = state.accountId,
    provider = account().provider;
  let catalog = state.catalog,
    seq = 0,
    catalogController,
    loadingCatalog = false;
  openDialog(
    "创建服务器",
    `<form id="create-form"><div class="error-box" role="alert"></div><p class="sub">${esc(providers[provider])} · ${esc(account().name)}</p><div class="section-title"><span class="step">1</span>地区与配置</div><label class="field">服务器名称<input name="name" pattern="[A-Za-z0-9-]{1,63}" maxlength="63" required placeholder="例如：dev-server-01"></label><label class="field">搜索地区<input name="regionSearch" type="search" placeholder="输入中文位置或原始地区代码"></label><div class="grid2"><label class="field">地区<select name="region" required>${groupedRegions(catalog?.regions, state.region, "正在加载地区…")}</select></label><label class="field">规格<select name="plan" required disabled><option value="">先选择地区</option></select></label></div><div id="plan-info" class="notice">先选择地区，查询该地区可用的CPU、内存、磁盘和价格。</div><button type="button" class="quiet" id="retry-catalog" disabled>重新查询（将重置配置选择）</button><label class="field">搜索系统镜像<input name="imageSearch" placeholder="Ubuntu、Debian、Windows Server…" type="search"></label><label class="field">系统镜像<select name="image" required disabled><option value="">先选择地区和规格</option></select><p class="help">优先展示上游维护中的版本。维护期来源于官方发行数据；无法核实时标记未确认。</p><p id="image-compatibility" class="help"></p></label><label class="check"><input name="showOlder" type="checkbox">显示维护结束或尚未发布的镜像</label><div id="network-fields"></div><div class="section-title"><span class="step">2</span>登录与启动</div><div id="access-fields"></div>${provider === "linode" ? firewallFields() : ""}<details><summary>更多启动选项</summary>${["aws", "digitalocean"].includes(provider) ? '<label class="field">初始化脚本（可选）<textarea name="userData" maxlength="32768" placeholder="#!/bin/sh"></textarea></label>' : ""}${["aws", "digitalocean"].includes(provider) ? '<label class="check"><input name="ipv6" type="checkbox">分配 IPv6（需要地区及网络支持）</label>' : ""}${["linode", "digitalocean"].includes(provider) ? '<label class="check"><input name="backups" type="checkbox">启用云厂商自动备份（可能额外收费）</label>' : ""}${provider === "azure" ? '<label class="field">系统盘容量 GB（留空使用镜像默认值）<input name="disk" type="number" min="1" max="4095"></label>' : ""}</details><div class="section-title"><span class="step">3</span>核对配置</div><dl id="create-summary" class="summary" aria-live="polite"></dl><div class="notice">创建会产生云服务费用。提交后可在操作任务中查看进度。${provider === "azure" ? "将自动创建公网 IP 和网卡，资源名称附加任务后缀。所选子网需配置合适的安全规则。" : ""}</div><label class="field">再次输入服务器名称确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="primary" type="submit" disabled>确认创建</button></div></form>`,
  );
  const f = dialog.querySelector("form");
  const selectedAccount = account();
  if (provider === "linode") loadFirewallOptions(f, aid);
  dialog.addEventListener(
    "close",
    () => {
      ++seq;
      catalogController?.abort();
    },
    { once: true },
  );
  function summary() {
    f.querySelector("#retry-catalog").disabled =
      !f.region.value || loadingCatalog;
    const p = catalog?.plans?.find((p) => p.id === f.plan.value);
    const im = catalog?.images?.find((im) => im.id === f.image.value);
    const network = catalog?.networks?.find(
      (n) => n.id === f.elements.network?.value,
    );
    const group = catalog?.groups?.find(
      (n) => n.id === f.elements.group?.value,
    );
    f.querySelector("#plan-info").textContent = p
      ? `${configLabel(p, provider)} · 型号 ${p.id}。${priceLabel(p.pricing)}${p.pricing?.source ? " 来源：" + p.pricing.source : ""}。独立磁盘、公网IP、备份、流量和系统授权可能额外收费。`
      : catalog?.plans?.length
        ? "请选择配置；vCPU是云厂商分配的虚拟处理器数量。"
        : "先选择地区查询可用规格；没有可用规格时可换地区或重试。";
    const fields = [
      ["云厂商 / 账号", providers[provider] + " · " + selectedAccount.name],
      [
        "地区",
        f.region.value
          ? regionLabel(catalog?.regions, f.region.value)
          : "尚未选择",
      ],
      ["服务器名称", f.elements.name.value || "尚未填写"],
      ...(provider === "linode"
        ? [
            [
              "云防火墙",
              {
                all: "全部放行（默认）",
                existing: "使用已有防火墙",
                custom: "自定义限制规则",
              }[f.elements.firewallMode.value] +
                (f.elements.allowUnverified.checked
                  ? "；创建后人工核实，不保证已放行"
                  : ""),
            ],
          ]
        : []),
      ["配置", p ? configLabel(p, provider) + " · " + p.id : "尚未选择"],
      ["系统", im?.name || "尚未选择"],
      ["CPU 架构", p?.architecture || im?.architecture || "未确认"],
      [
        "系统盘",
        ["aws", "azure"].includes(provider)
          ? f.elements.disk?.value
            ? f.elements.disk.value + " GB（独立计费）"
            : im?.disk
              ? "镜像最低 " + im.disk + " GB；实际系统盘按云厂商配置计费"
              : "使用镜像默认系统盘，容量及费用以云厂商为准"
          : p?.disk
            ? p.disk + " GB 套餐磁盘，系统与交换分区共享"
            : "容量未确认",
      ],
      [
        "网络",
        [network?.name, group?.name].filter(Boolean).join(" / ") ||
          (["aws", "azure"].includes(provider)
            ? "尚未选择网络"
            : "云厂商默认网络"),
      ],
      [
        "登录方式",
        f.elements.key?.value || f.elements.sshPublicKey?.value
          ? "SSH 密钥（不展示密钥内容）"
          : f.elements.password?.value
            ? "密码登录（不展示密码）"
            : "尚未配置",
      ],
      ["广告价格", priceLabel(p?.pricing)],
    ];
    f.querySelector("#create-summary").innerHTML = fields
      .map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`)
      .join("");
  }
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
          (!["linode", "digitalocean"].includes(provider) ||
            !plan?.disk ||
            !i.disk ||
            plan.disk >= i.disk) &&
          (!q ||
            i.name.toLowerCase().includes(q) ||
            (i.family || "").includes(q)),
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
          (a.family || "").localeCompare(b.family || "") ||
          b.name.localeCompare(a.name, undefined, { numeric: true }),
      );
    f.image.innerHTML = options(ims, previous, "选择可用系统镜像");
    f.image.disabled = !plan || !ims.length;
    f.querySelector("button[type=submit]").disabled = !plan || !ims.length;
    f.querySelector("#image-compatibility").textContent = !plan
      ? "先选择配置，系统列表会按CPU架构和磁盘容量筛选。"
      : !ims.length
        ? "没有匹配的系统；可清除搜索、切换配置或查看较旧镜像。"
        : "已按架构、磁盘容量和维护状态筛选，选择时仍会向云厂商重新校验。";
    summary();
  }
  async function load() {
    const current = ++seq;
    loadingCatalog = true;
    catalogController?.abort();
    catalogController = new AbortController();
    const regions = catalog?.regions || [];
    catalog = {
      regions,
      plans: [],
      images: [],
      networks: [],
      groups: [],
      keys: [],
    };
    f.plan.innerHTML = options([], "", "正在查询可用配置…");
    f.image.innerHTML = options([], "", "等待配置查询…");
    f.querySelector("#network-fields").innerHTML = "";
    access();
    summary();
    f.querySelector("button[type=submit]").disabled = true;
    f.plan.disabled = true;
    f.image.disabled = true;
    f.querySelector(".error-box").textContent = "";
    try {
      const nextCatalog = await api(
        "catalog?" +
          new URLSearchParams({ accountId: aid, region: f.region.value }),
        { signal: catalogController.signal },
      );
      if (
        current !== seq ||
        !dialog.open ||
        !f.isConnected ||
        state.accountId !== aid
      )
        return;
      catalog = nextCatalog;
      f.plan.innerHTML =
        '<option value="">选择CPU、内存和磁盘配置</option>' +
        catalog.plans
          .map(
            (p) =>
              `<option value="${esc(p.id)}">${esc(configLabel(p, provider) + " · " + p.id + " · " + priceLabel(p.pricing))}</option>`,
          )
          .join("");
      f.plan.disabled = !catalog.plans.length;
      f.querySelector("#network-fields").innerHTML = ["aws", "azure"].includes(
        provider,
      )
        ? `<label class="field">${provider === "aws" ? "子网" : "已有子网"}<select name="network" required>${options(catalog.networks)}</select></label>${provider === "aws" ? `<label class="field">安全组<select name="group" required>${options(catalog.groups)}</select><p class="help">需要与子网属于同一 VPC；请只向可信地址开放管理端口。</p></label>` : '<p class="help">使用当前资源组内已有 VNet 子网。不会修改或删除已有网络。</p>'}`
        : "";
      access();
      images();
    } catch (e) {
      if (current === seq && f.isConnected)
        f.querySelector(".error-box").textContent = e.message;
    } finally {
      if (current === seq && f.isConnected) {
        loadingCatalog = false;
        summary();
      }
    }
  }
  f.querySelector("#retry-catalog").onclick = () => {
    if (f.region.value) load();
  };
  access();
  f.region.onchange = () => {
    if (f.region.value) load();
    else {
      ++seq;
      loadingCatalog = false;
      catalogController?.abort();
      catalog = { regions: catalog?.regions || [], plans: [], images: [] };
      f.querySelector("#network-fields").innerHTML = "";
      f.plan.innerHTML = options([], "", "先选择地区");
      f.plan.disabled = true;
      images();
      access();
    }
  };
  f.regionSearch.oninput = () => {
    f.region.innerHTML = groupedRegions(
      regionChoices(catalog?.regions, f.regionSearch.value, f.region.value),
      f.region.value,
      "选择地区",
    );
  };
  f.addEventListener("input", summary);
  f.addEventListener("change", summary);
  f.plan.onchange = images;
  f.imageSearch.oninput = images;
  f.showOlder.onchange = images;
  f.image.onchange = summary;
  summary();
  f.onsubmit = (e) => {
    e.preventDefault();
    formAction(f, async () => {
      const data = Object.fromEntries(new FormData(f));
      const im = catalog.images.find((i) => i.id === data.image);
      const spec = {
        ...(provider === "linode"
          ? { firewall: readFirewallSelection(f) }
          : {}),
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
      if (!dialog.open || !f.isConnected || state.accountId !== aid) return;
      f.region.innerHTML = groupedRegions(catalog.regions, state.region);
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
  const intentId = intentID();
  if (!intentId || !action) return;
  const selectedAccount = account();
  const aid = selectedAccount.id;
  const provider = selectedAccount.provider;
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
    `<form><div class="error-box" role="alert"></div><div class="notice">${esc(warning)}</div><dl class="summary"><dt>服务器</dt><dd>${esc(i.name)}</dd><dt>云厂商 / 账号</dt><dd>${esc(providers[provider] + " · " + selectedAccount.name)}</dd><dt>地区</dt><dd>${esc(regionLabel(currentInventory()?.regions, i.region, i.regionName))}</dd><dt>资源标识</dt><dd class="mono">${esc(i.id)}</dd></dl><label class="field">输入服务器名称「${esc(i.name)}」确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="${action === "delete" ? "danger" : "primary"}" type="submit">确认${labels[action]}</button></div></form>`,
  );
  const f = dialog.querySelector("form");
  f.onsubmit = (e) => {
    e.preventDefault();
    formAction(f, async () => {
      await submit({
        id: intentId,
        accountId: aid,
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
    const previous = new Map(state.jobs.map((j) => [j.id, j.state]));
    state.jobs = await api("jobs");
    const completed = new Set(
      state.jobs
        .filter(
          (j) =>
            previous.has(j.id) &&
            previous.get(j.id) !== j.state &&
            ["succeeded", "failed", "unknown", "cancelled"].includes(j.state),
        )
        .map((j) => j.accountId),
    );
    for (const aid of completed) {
      inventories.delete(aid);
      queueInventory(aid, { summary: aid !== state.accountId }).catch(() => {});
    }
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
  if (state.accountId) refresh();
} catch {
  login();
}
setInterval(() => {
  if (state.csrf && !document.hidden) updateJobs();
}, 5000);

initLinode({
  api,
  esc,
  options,
  openDialog,
  dialog,
  intentID,
  submit,
  formAction,
  account: () => account(),
  accounts: () => state.accounts,
  toast,
});

async function showLinodePermissions(acc) {
  openDialog("Linode权限检查", "<p>正在查询…</p>");
  const pane = dialog.querySelector(".dialog-body");
  try {
    const r = await api(
      "linode?" +
        new URLSearchParams({ accountId: acc.id, view: "permissions" }),
    );
    if (
      !pane.isConnected ||
      !state.accounts.some(
        (a) => a.id === acc.id && a.revision === r.accountRevision,
      )
    )
      return;
    pane.innerHTML =
      r.data
        .map(
          (p) =>
            `<p>${esc(p.name)}：${esc(p.status)}<br><span class="help">${esc(p.scope)} · ${esc(p.write)}</span></p>`,
        )
        .join("") +
      `<p class="help">高级操作完成核实还需events:read_only。缺少账单读取权限不影响服务器管理；无需支付相关写权限。</p>`;
  } catch (e) {
    if (pane.isConnected)
      pane.innerHTML = `<p class="error-box">${esc(e.message)}</p>`;
  }
}
