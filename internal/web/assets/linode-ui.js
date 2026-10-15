import {
  configLabel,
  priceLabel,
  serverStateLabel,
  regionLabel,
} from "./format.js";
let ctx;
export function initLinode(value) {
  ctx = value;
}
const continents = [
  "亚洲",
  "欧洲",
  "北美洲",
  "南美洲",
  "大洋洲",
  "非洲",
  "其他／位置待确认",
];
const escape = (s) =>
  String(s ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
export function groupedRegions(
  items = [],
  value = "",
  placeholder = "选择地区",
) {
  return (
    `<option value="">${escape(placeholder)}</option>` +
    continents
      .map((group) => {
        const rows = (items || []).filter(
          (i) => (i.continent || "其他／位置待确认") === group,
        );
        return rows.length
          ? `<optgroup label="${group}">${rows.map((i) => `<option value="${escape(i.id)}" ${i.id === value ? "selected" : ""}>${escape(i.name)}</option>`).join("")}</optgroup>`
          : "";
      })
      .join("")
  );
}
const rulesEditor = () =>
  `<div class="firewall-rule-fields"><p class="help">规则按顺序匹配；修改将替换完整双向规则。</p><div class="grid2"><label class="field">入站默认<select name="inPolicy"><option value="DROP">拒绝</option><option value="ACCEPT">允许</option></select></label><label class="field">出站默认<select name="outPolicy"><option value="ACCEPT">允许</option><option value="DROP">拒绝</option></select></label></div><div class="rule-rows"></div><button type="button" data-add-rule>＋添加规则</button></div>`;
export function firewallFields() {
  return `<fieldset class="firewall-picker"><legend>云防火墙</legend><label class="field">访问策略<select name="firewallMode"><option value="all">全部放行（默认）</option><option value="existing">使用已有云防火墙</option><option value="custom">自定义限制规则</option></select></label><p class="help">允许公网访问服务器上所有正在监听的端口。建议仅在了解风险时使用；操作系统自身防火墙规则不受此设置控制。</p><div data-existing hidden><label class="field">已有防火墙<select name="firewallID"><option value="">加载后选择</option></select></label></div><div data-custom hidden>${rulesEditor()}</div><label class="check" data-unverified hidden><input name="allowUnverified" type="checkbox">防火墙权限不足时，仍创建并到云控制台人工核实（不宣称已放行）</label><p class="error-box" data-firewall-error></p></fieldset>`;
}
function addRule(form, value = {}) {
  const row = document.createElement("div");
  row.className = "firewall-rule-row";
  row.innerHTML = `<label>方向<select data-direction><option value="inbound">入站</option><option value="outbound">出站</option></select></label><label>操作<select data-rule-action><option value="ACCEPT">允许</option><option value="DROP">拒绝</option></select></label><label>协议<select data-protocol>${["TCP", "UDP", "ICMP", "IPENCAP"].map((p) => `<option>${p}</option>`).join("")}</select></label><label>端口<input data-ports placeholder="22,80,443 或 1000-2000"></label><label>IPv4 CIDR<input data-v4 placeholder="0.0.0.0/0；多个用逗号分隔"></label><label>IPv6 CIDR<input data-v6 placeholder="::/0；多个用逗号分隔"></label><button type="button" data-remove-rule>删除规则</button>`;
  form.querySelector(".rule-rows").append(row);
  const set = (selector, v) => (row.querySelector(selector).value = v);
  set("[data-direction]", value.direction || "inbound");
  set("[data-rule-action]", value.action || "ACCEPT");
  set("[data-protocol]", value.protocol || "TCP");
  set("[data-ports]", value.ports || "");
  set("[data-v4]", (value.addresses?.ipv4 || ["0.0.0.0/0"]).join(","));
  set("[data-v6]", (value.addresses?.ipv6 || ["::/0"]).join(","));
  row.querySelector("[data-remove-rule]").onclick = () => row.remove();
  row.querySelector("[data-protocol]").onchange = () => {
    const p = row.querySelector("[data-protocol]").value;
    row.querySelector("[data-ports]").disabled = !["TCP", "UDP"].includes(p);
    if (!["TCP", "UDP"].includes(p)) set("[data-ports]", "");
  };
}
function readRules(form) {
  const rules = {
    inbound: [],
    outbound: [],
    inbound_policy: form.elements.inPolicy.value,
    outbound_policy: form.elements.outPolicy.value,
  };
  for (const row of form.querySelectorAll(".firewall-rule-row")) {
    const get = (q) => row.querySelector(q).value;
    const list = (q) =>
      get(q)
        .split(",")
        .map((v) => v.trim())
        .filter(Boolean);
    rules[get("[data-direction]")].push({
      action: get("[data-rule-action]"),
      protocol: get("[data-protocol]"),
      ports: get("[data-ports]"),
      addresses: { ipv4: list("[data-v4]"), ipv6: list("[data-v6]") },
    });
  }
  return rules;
}
export function readFirewallSelection(form) {
  if (form.querySelector(".firewall-picker")?.dataset.invalid === "true")
    firewallDataError();
  const mode = form.elements.firewallMode.value;
  return {
    mode,
    ...(mode === "existing"
      ? {
          id: Number(form.elements.firewallID.value),
          snapshot:
            form.elements.firewallID.selectedOptions[0]?.dataset.snapshot || "",
        }
      : {}),
    ...(mode === "custom" ? { rules: readRules(form) } : {}),
    ...(mode === "all"
      ? { allowUnverified: !!form.elements.allowUnverified.checked }
      : {}),
  };
}
// Normalize only explicit empty collections. Missing/incorrect security fields
// must not become an apparently permissive firewall or an unbound resource.
function firewallDataError() {
  throw new Error(
    "防火墙数据不完整或格式无效，无法确认规则与绑定范围；请重试或到云控制台核实",
  );
}
const record = (v) => v !== null && typeof v === "object" && !Array.isArray(v);
const positiveID = (v) => Number.isSafeInteger(v) && v > 0;
function collection(v) {
  if (v === null) return [];
  if (!Array.isArray(v)) firewallDataError();
  return v;
}
function firewallRules(v) {
  if (
    !record(v) ||
    !["ACCEPT", "DROP"].includes(v.inbound_policy) ||
    !["ACCEPT", "DROP"].includes(v.outbound_policy)
  )
    firewallDataError();
  const out = { ...v };
  for (const dir of ["inbound", "outbound"]) {
    out[dir] = collection(v[dir]).map((r) => {
      if (
        !record(r) ||
        !["ACCEPT", "DROP"].includes(r.action) ||
        !["TCP", "UDP", "ICMP", "IPENCAP"].includes(r.protocol) ||
        !record(r.addresses) ||
        (r.ports !== undefined && typeof r.ports !== "string")
      )
        firewallDataError();
      const addresses = {};
      for (const family of ["ipv4", "ipv6"]) {
        addresses[family] =
          r.addresses[family] === undefined
            ? []
            : collection(r.addresses[family]);
        if (
          addresses[family].some((ip) => typeof ip !== "string" || !ip.trim())
        )
          firewallDataError();
      }
      if (!addresses.ipv4.length && !addresses.ipv6.length) firewallDataError();
      return { ...r, addresses };
    });
  }
  return out;
}
function firewallRecord(f) {
  if (
    !record(f) ||
    !positiveID(f.id) ||
    typeof f.label !== "string" ||
    !f.label.trim() ||
    !["enabled", "disabled"].includes(f.status) ||
    typeof f.snapshot !== "string" ||
    !f.snapshot
  )
    firewallDataError();
  const devices = collection(f.devices);
  const seen = new Set();
  for (const d of devices) {
    if (
      !record(d) ||
      !positiveID(d.id) ||
      seen.has(d.id) ||
      !record(d.entity) ||
      !positiveID(d.entity.id) ||
      !["linode", "linode_interface", "nodebalancer"].includes(d.entity.type) ||
      (d.entity.label !== undefined && typeof d.entity.label !== "string")
    )
      firewallDataError();
    seen.add(d.id);
    if (
      d.entity.type === "linode_interface" &&
      (!record(d.entity.parent_entity) ||
        !positiveID(d.entity.parent_entity.id) ||
        d.entity.parent_entity.type !== "linode")
    )
      firewallDataError();
  }
  return { ...f, rules: firewallRules(f.rules), devices };
}
function firewallView(data, full) {
  if (!record(data) || typeof data.snapshot !== "string" || !data.snapshot)
    firewallDataError();
  const seen = new Set();
  const items = collection(data.items).map((f) => {
    if (
      !record(f) ||
      !positiveID(f.id) ||
      seen.has(f.id) ||
      typeof f.label !== "string" ||
      !f.label.trim() ||
      !["enabled", "disabled"].includes(f.status)
    )
      firewallDataError();
    seen.add(f.id);
    return full ? firewallRecord(f) : f;
  });
  return { ...data, items };
}
async function read(acc, view, parameters = {}) {
  const reply = await ctx.api(
    "linode?" + new URLSearchParams({ accountId: acc.id, view, ...parameters }),
  );
  if (
    reply.accountId !== acc.id ||
    reply.accountRevision !== acc.revision ||
    !ctx.accounts().some((a) => a.id === acc.id && a.revision === acc.revision)
  )
    throw new Error("账号已变化，请重新打开详情");
  if (view === "firewalls") return firewallView(reply.data, true);
  if (view === "instance-firewalls") return firewallView(reply.data, false);
  if (view === "firewall") return firewallRecord(reply.data);
  return reply.data;
}
export async function loadFirewallOptions(form, aid) {
  const picker = form.querySelector(".firewall-picker");
  if (!picker) return;
  picker.dataset.invalid = "true";
  const acc = ctx.accounts().find((a) => a.id === aid);
  const show = () => {
    const mode = form.elements.firewallMode.value;
    picker.querySelector("[data-existing]").hidden = mode !== "existing";
    picker.querySelector("[data-custom]").hidden = mode !== "custom";
  };
  form.elements.firewallMode.onchange = show;
  show();
  form.querySelector("[data-add-rule]").onclick = () => addRule(form);
  try {
    const v = await read(acc, "firewalls");
    if (!form.isConnected) return;
    picker.dataset.invalid = "false";
    form.elements.firewallID.innerHTML = ctx.options(
      v.items
        .filter((f) => f.status === "enabled")
        .map((f) => ({
          id: String(f.id),
          name: f.label + ` · ${f.devices.length}个绑定`,
        })),
    );
    for (const option of form.elements.firewallID.options) {
      const f = v.items.find((f) => String(f.id) === option.value);
      if (f) option.dataset.snapshot = f.snapshot;
    }
  } catch (e) {
    if (form.isConnected) {
      picker.querySelector("[data-firewall-error]").textContent = e.message;
      const permissionFailure = /权限/.test(e.message);
      picker.dataset.invalid = String(!permissionFailure);
      picker.querySelector("[data-unverified]").hidden = !permissionFailure;
    }
  }
}
const when = (s) =>
  new Date(s * 1000).toLocaleString("zh-CN", { hour12: false });
const val = (n, unit = "") =>
  n === null || n === undefined
    ? "官方未提供"
    : `${Number(n).toLocaleString("zh-CN", { maximumFractionDigits: 3 })} ${unit}`;
const bytes = (n) =>
  n === null || n === undefined
    ? "官方未提供"
    : `${val(n / 2 ** 30, "GiB")}（${val(n, "bytes")}）`;
const warnings = {
  rebuild:
    "重装将永久删除原本地磁盘数据及配置，请先完成备份；不是删除后重新创建服务器。告警设置需重新检查。",
  resize:
    "冷迁移可能停机并迁移宿主机。不会自动缩小磁盘或删除数据；附加Volume与特殊托管关系需先在控制台处理。",
  "password-reset":
    "需要服务器完全关机。只重置选定系统磁盘的root密码，不自动关机或开机。",
  clone:
    "会创建新的计费实例。源服务器需先关机保证副本一致；克隆磁盘和配置，不复制附加Volume、锁或placement group。新机保持关机，防火墙仅调整新机。",
  migrate:
    "跨数据中心迁移可能停机，公网IPv4/IPv6可能变化，旧备份不迁移。请人工检查DNS；Narbe不会更改外部DNS。",
  "firewall-bind":
    "只调整选中的实例或接口绑定，可能中断SSH及业务连接，不修改其他服务器或账户全局默认。",
};
function instanceSummary(s, acc) {
  const i = s.instance;
  return `<dl class="summary"><dt>云账号</dt><dd>Linode · ${escape(acc.name)}</dd><dt>服务器 / ID</dt><dd>${escape(i.name)} · ${escape(i.id)}</dd><dt>地区</dt><dd>${escape(regionLabel([], i.region, i.regionName))}</dd><dt>状态</dt><dd>${escape(serverStateLabel(i.state))}</dd><dt>配置</dt><dd>${escape(configLabel(i, "linode"))} · ${escape(i.plan)}</dd><dt>系统</dt><dd>${escape(i.image || "系统未确认")}</dd><dt>公网IP</dt><dd>${escape(i.ips.join(" / ") || "未分配或未确认")}</dd></dl>`;
}
export async function linodeDetails(i, acc, v) {
  ctx.openDialog(
    "Linode服务器详情",
    `<p class="loading">正在读取服务器详情…</p>`,
  );
  const container = ctx.dialog.querySelector(".dialog-body");
  try {
    const s = await read(acc, "instance", { target: i.id });
    if (!container.isConnected) return;
    s.instance.regionName = regionLabel(v.regions, i.region, i.regionName);
    container.innerHTML = `${instanceSummary(s, acc)}<div class="detail-tabs"><button data-detail="manage">管理</button><button data-detail="transfer">流量</button><button data-detail="stats">运行监控</button><button data-detail="firewall-bind">云防火墙</button></div><div class="detail-content"></div>`;
    const pane = container.querySelector(".detail-content");
    let seq = 0;
    const show = async (view) => {
      const current = ++seq;
      pane.innerHTML = '<p class="loading">正在查询…</p>';
      try {
        if (view === "manage") {
          pane.innerHTML = `<p class="help">选择操作后查看原配置与目标差异，再确认。</p><div class="management-actions">${["rebuild", "resize", "password-reset", "clone", "migrate"].map((a) => `<button data-advanced="${a}">${{ rebuild: "重装系统", resize: "升级 / 降低配置", "password-reset": "重置root密码", clone: "克隆新服务器", migrate: "迁移数据中心" }[a]}</button>`).join("")}</div>`;
          pane
            .querySelectorAll("[data-advanced]")
            .forEach(
              (b) =>
                (b.onclick = () => advancedDialog(s, acc, b.dataset.advanced)),
            );
          return;
        }
        if (view === "firewall-bind") {
          await advancedDialog(s, acc, view);
          return;
        }
        await metricsPane(
          pane,
          acc,
          i.id,
          view,
          () => current === seq && container.isConnected,
        );
      } catch (e) {
        if (current === seq && pane.isConnected)
          pane.innerHTML = `<p class="error-box">${escape(e.message)}</p><button data-read-retry>重试</button>`;
        pane
          .querySelector("[data-read-retry]")
          ?.addEventListener("click", () => show(view));
      }
    };
    container
      .querySelectorAll("[data-detail]")
      .forEach((b) => (b.onclick = () => show(b.dataset.detail)));
    show("manage");
  } catch (e) {
    if (container.isConnected)
      container.innerHTML = `<p class="error-box">${escape(e.message)}</p><button data-retry>重试详情</button>`;
    container
      .querySelector("[data-retry]")
      ?.addEventListener("click", () => linodeDetails(i, acc, v));
  }
}
function chart(series) {
  if (!series.points?.length)
    return `<article class="metric-chart"><h3>${escape(series.label)} · ${escape(series.unit)}</h3><p>暂无监控数据</p></article>`;
  const points = series.points,
    width = 600,
    height = 130,
    min = points[0][0],
    span = points.at(-1)[0] - min || 1,
    max = Math.max(...points.map((p) => p[1])),
    scale = Math.max(max, 1);
  const step = Math.max(1, Math.ceil(points.length / 600));
  const shown = points.filter(
    (_, i) => i % step === 0 || i === points.length - 1,
  );
  const coords = shown
    .map(
      (p) =>
        `${10 + ((p[0] - min) / span) * 580},${120 - (p[1] / scale) * 110}`,
    )
    .join(" ");
  return `<article class="metric-chart"><h3>${escape(series.label)} · ${escape(series.unit)}</h3><svg viewBox="0 0 ${width} ${height}" role="img" aria-label="${escape(series.label)}真实监控折线"><line x1="10" y1="120" x2="590" y2="120" class="chart-axis"/><polyline points="${coords}" class="chart-line"/></svg><div class="chart-caption">${new Date(min).toLocaleString("zh-CN")} — ${new Date(points.at(-1)[0]).toLocaleString("zh-CN")} · 最大 ${val(max, series.unit)} · ${series.sampleSeconds ? `采样${series.sampleSeconds}秒` : "采样间隔不规则或未确认"}${step > 1 ? " · 图表抽样显示，原始点未改动" : ""}</div></article>`;
}
async function metricsPane(pane, acc, id, view, alive) {
  pane.innerHTML = `<div class="row-actions"><label>统计月份<input type="month" data-period max="${new Date().toISOString().slice(0, 7)}"></label><button data-query>查询</button><span class="help">留空：${view === "stats" ? "过去24小时" : "当前自然月"}</span></div><div data-result></div>`;
  const result = pane.querySelector("[data-result]");
  let seq = 0;
  const load = async () => {
    const mine = ++seq;
    result.innerHTML = '<p class="loading">正在查询…</p>';
    try {
      let period = pane.querySelector("[data-period]").value;
      if (
        view === "transfer" &&
        period === new Date().toISOString().slice(0, 7)
      )
        period = "";
      const data = await read(acc, view, { target: id, period });
      if (!alive() || mine !== seq || !result.isConnected) return;
      if (view === "stats")
        result.innerHTML = `<p class="help">${escape(data.source)} · ${escape(data.period)} · 查询于${when(data.queriedAt)}；时间按本地时区显示</p><div class="charts">${data.series.map(chart).join("")}</div>`;
      else
        result.innerHTML = `<p class="help">${escape(data.source)} · ${escape(data.period)} · 查询于${when(data.queriedAt)}</p><dl class="summary"><dt>本实例当月已用</dt><dd>${bytes(data.usedBytes ?? data.totalBytes)}</dd><dt>本实例贡献流量额度</dt><dd>${val(data.quotaGB, "GiB（官方GB）")}</dd><dt>官方报告计费量</dt><dd>${val(data.billableGB, "GiB（官方GB）")}</dd><dt>入站 / 出站</dt><dd>${bytes(data.inBytes)} / ${bytes(data.outBytes)}</dd></dl><p class="notice">实例额度贡献给共享流量池，不是独享剩余额度；是否收费以官方报告及最终账单为准。</p>${data.accountPool ? `<h3>账户共享池（当前月）</h3><p>已用 ${val(data.accountPool.usedGB, "GiB")} / 总额度 ${val(data.accountPool.quotaGB, "GiB")} · 官方计费量 ${val(data.accountPool.billableGB, "GiB")}</p>` : `<p class="help">${escape(data.accountError || "历史月未查询当前账户共享池")}</p>`}`;
    } catch (e) {
      if (alive() && mine === seq)
        result.innerHTML = `<p class="error-box">${escape(e.message)}</p>`;
    }
  };
  pane.querySelector("[data-query]").onclick = load;
  await load();
}
async function advancedDialog(s, acc, action) {
  const id = ctx.intentID();
  if (!id) return;
  let catalog = null;
  let gen = 0;
  const needsCatalog = ["rebuild", "resize", "clone", "migrate"].includes(
    action,
  );
  const source = s.instance;
  const title = {
    rebuild: "重装系统",
    resize: "调整服务器配置",
    "password-reset": "重置root密码",
    clone: "克隆为新服务器",
    migrate: "迁移数据中心",
    "firewall-bind": "调整云防火墙",
  }[action];
  let fields = "";
  if (["clone", "migrate"].includes(action))
    fields += `<label class="field">${action === "clone" ? '新服务器名称<input name="newLabel" required minlength="3" maxlength="32">' : ""}</label><label class="field">搜索地区<input name="regionSearch" type="search" placeholder="中文位置或地区代码"></label><label class="field">目标地区<select name="newRegion" required></select></label>`;
  if (["resize", "clone"].includes(action))
    fields += `<label class="field">目标配置<select name="newPlan" required></select></label>`;
  if (action === "rebuild")
    fields += `<label class="field">重装系统镜像<select name="newImage" required></select></label><label class="field">新root密码（与SSH公钥至少选一项）<input name="password" type="password" autocomplete="new-password" maxlength="128"></label><label class="field">SSH公钥<textarea name="publicKey" placeholder="ssh-ed25519 …"></textarea></label><label class="field">已有SSH密钥<select name="newKey"></select></label>`;
  if (action === "password-reset")
    fields += `<label class="field">系统磁盘<select name="newDisk" required>${ctx.options(s.disks.filter((d) => ["ext3", "ext4"].includes(d.filesystem)).map((d) => ({ id: String(d.id), name: d.label + ` · ${d.size} MB · ${d.filesystem}` })))}</select></label><label class="field">新root密码<input name="password" type="password" autocomplete="new-password" minlength="11" maxlength="128" required></label><p class="help">避免常见、重复或易猜密码；最终强度由Linode检查。多个系统盘必须明确选择要修改的磁盘。</p>`;
  if (action === "clone" || action === "firewall-bind")
    fields += firewallFields();
  if (action === "firewall-bind" && s.generation === "linode")
    fields += `<label class="field">网络接口<select name="newInterface" required>${ctx.options(s.interfaces.filter((i) => i.public || i.vpc).map((i) => ({ id: String(i.id), name: `${i.id} · ${i.public ? "公网Public" : "VPC"}` })))}</select></label>`;
  ctx.openDialog(
    title,
    `<form class="advanced-form"><div class="error-box" role="alert"></div>${instanceSummary(s, acc)}<p class="notice">${warnings[action]}</p>${fields}<div class="target-comparison"></div><label class="check"><input name="acknowledge" type="checkbox" required>已了解并确认以上影响</label><label class="field">输入服务器名称「${escape(source.name)}」确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="danger" type="submit" ${needsCatalog ? "disabled" : ""}>核实并确认${title}</button></div></form>`,
  );
  const f = ctx.dialog.querySelector("form");
  if (action === "clone" || action === "firewall-bind")
    loadFirewallOptions(f, acc.id);
  let binding = "";
  const show = () => {
    const plan = catalog?.plans.find(
      (p) => p.id === (f.elements.newPlan?.value || source.plan),
    );
    f.querySelector(".target-comparison").innerHTML =
      `<h3>原配置 → 目标配置</h3><p>${escape(configLabel(source, "linode"))} → ${escape(plan ? configLabel(plan, "linode") : "配置未确认")}</p><p>目标广告价格：${escape(priceLabel(plan?.pricing))}</p>${action === "clone" ? '<p class="notice">新增计费实例，源实例保持不变；克隆新机需手动开机。</p>' : ""}`;
  };
  const load = async () => {
    const n = ++gen;
    f.querySelector("button[type=submit]").disabled = true;
    for (const name of ["newPlan", "newImage", "newKey"]) {
      if (f.elements[name])
        f.elements[name].innerHTML = ctx.options([], "", "正在查询…");
    }
    f.querySelector(".error-box").textContent = "";
    try {
      const region = f.elements.newRegion?.value || source.region;
      catalog = await ctx.api(
        "catalog?" + new URLSearchParams({ accountId: acc.id, region }),
      );
      if (!f.isConnected || n !== gen) return;
      if (f.elements.newRegion && !f.elements.newRegion.value) {
        f.elements.newRegion.innerHTML = groupedRegions(catalog.regions);
        return;
      }
      if (f.elements.newPlan)
        f.elements.newPlan.innerHTML = ctx.options(
          catalog.plans.map((p) => ({
            ...p,
            name:
              configLabel(p, "linode") +
              " · " +
              p.id +
              " · " +
              priceLabel(p.pricing),
          })),
        );
      if (f.elements.newImage)
        f.elements.newImage.innerHTML = ctx.options(
          catalog.images.filter(
            (im) => !im.deprecated && (!im.disk || im.disk <= source.disk),
          ),
        );
      if (f.elements.newKey)
        f.elements.newKey.innerHTML = ctx.options(
          catalog.keys,
          "",
          "不使用已有密钥",
        );
      show();
      f.querySelector("button[type=submit]").disabled = false;
    } catch (e) {
      if (f.isConnected && n === gen)
        f.querySelector(".error-box").textContent = e.message;
    }
  };
  if (f.elements.newRegion) {
    try {
      const c = await ctx.api(
        "catalog?accountId=" + encodeURIComponent(acc.id),
      );
      if (!f.isConnected) return;
      catalog = c;
      f.elements.newRegion.innerHTML = groupedRegions(
        c.regions.filter((r) => action !== "migrate" || r.id !== source.region),
      );
      f.elements.newRegion.onchange = load;
      f.elements.regionSearch.oninput = () => {
        const selected = f.elements.newRegion.value,
          q = f.elements.regionSearch.value.toLowerCase();
        f.elements.newRegion.innerHTML = groupedRegions(
          c.regions.filter(
            (r) =>
              (action !== "migrate" || r.id !== source.region) &&
              (r.id === selected ||
                !q ||
                (r.name + r.id).toLowerCase().includes(q)),
          ),
          selected,
        );
      };
    } catch (e) {
      if (f.isConnected) f.querySelector(".error-box").textContent = e.message;
    }
  }
  if (f.elements.newPlan) f.elements.newPlan.onchange = show;
  if (action === "firewall-bind") {
    let bindingSequence = 0;
    const loadBinding = async () => {
      const mine = ++bindingSequence;
      binding = "";
      f.querySelector("button[type=submit]").disabled = true;
      try {
        if (f.elements.newInterface && !f.elements.newInterface.value) return;
        const list = await read(acc, "instance-firewalls", {
          target: source.id,
          ...(f.elements.newInterface?.value
            ? { interfaceId: f.elements.newInterface.value }
            : {}),
        });
        if (!f.isConnected || mine !== bindingSequence) return;
        binding = list.snapshot;
        f.querySelector("button[type=submit]").disabled = false;
      } catch (e) {
        if (f.isConnected && mine === bindingSequence)
          f.querySelector(".error-box").textContent = e.message;
      }
    };
    if (f.elements.newInterface) f.elements.newInterface.onchange = loadBinding;
    await loadBinding();
  }
  f.onsubmit = (e) => {
    e.preventDefault();
    ctx.formAction(f, async () => {
      const p = {
        snapshot: s.snapshot,
        acknowledge: f.elements.acknowledge.checked,
      };
      if (f.elements.password) p.password = f.elements.password.value;
      if (f.elements.publicKey) p.sshPublicKey = f.elements.publicKey.value;
      if (f.elements.newKey) p.key = f.elements.newKey.value;
      if (f.elements.newImage) p.image = f.elements.newImage.value;
      if (f.elements.newPlan) p.plan = f.elements.newPlan.value;
      if (f.elements.newRegion) p.region = f.elements.newRegion.value;
      if (f.elements.newLabel) p.label = f.elements.newLabel.value;
      if (f.elements.newDisk) p.diskId = Number(f.elements.newDisk.value);
      if (action === "clone" || action === "firewall-bind")
        p.firewall = readFirewallSelection(f);
      if (action === "firewall-bind") {
        p.interfaceId = Number(f.elements.newInterface?.value || 0);
        p.bindingSnapshot = binding;
      }
      await ctx.submit({
        id,
        accountId: acc.id,
        action,
        region: source.region,
        target: source.id,
        name: source.name,
        spec: {},
        advanced: p,
        confirmation: "",
        confirmName: f.elements.confirmName.value,
      });
      f.reset();
      ctx.dialog.close();
    });
  };
  if (needsCatalog && !["clone", "migrate"].includes(action)) await load();
}

export async function linodeAccount(acc, view = "billing") {
  ctx.openDialog(
    view === "billing" ? "Linode账单" : "Linode云防火墙",
    '<p class="loading">正在读取…</p>',
  );
  const pane = ctx.dialog.querySelector(".dialog-body");
  let generation = 0;
  const load = async (page = 1) => {
    const seq = ++generation;
    pane.innerHTML = '<p class="loading">正在读取…</p>';
    try {
      if (view === "billing") {
        const account = await read(acc, "billing");
        const data = await read(acc, "invoices", { page: String(page) });
        if (!pane.isConnected || seq !== generation) return;
        const balance = account.balance;
        const balanceText =
          balance === null || balance === undefined
            ? "余额未提供"
            : balance > 0
              ? `待付款余额 ${val(balance, "USD")}`
              : balance < 0
                ? `账户抵扣额度 ${val(-balance, "USD")}`
                : "当前无余额";
        pane.innerHTML = `<h3>${escape(acc.name)} · ${balanceText}</h3><p>未出账估算 ${val(account.uninvoiced, "USD")}</p><p class="notice">${escape(account.estimateNote)}；正余额不等于已逾期，付款状态以云控制台为准。</p><p class="help">${escape(account.source)} · 查询于${when(account.queriedAt)}</p><div class="table-wrap"><table><thead><tr><th>账单日期 / 名称</th><th>实际金额</th><th>状态</th><th></th></tr></thead><tbody>${data.invoices.data.map((i) => `<tr><td>${escape(i.date)}<br>${escape(i.label)}</td><td>${val(i.total, "USD")}</td><td>已生成；付款状态未提供</td><td><button data-invoice="${i.id}">详情</button></td></tr>`).join("")}</tbody></table></div>${!data.invoices.data.length ? "<p>暂无账单</p>" : ""}<div class="pagination"><button data-prev ${page <= 1 ? "disabled" : ""}>上一页</button><span>第${page}/${data.invoices.pages}页</span><button data-next ${page >= data.invoices.pages ? "disabled" : ""}>下一页</button></div><div data-invoice-detail></div>`;
        pane.querySelector("[data-prev]").onclick = () => load(page - 1);
        pane.querySelector("[data-next]").onclick = () => load(page + 1);
        pane
          .querySelectorAll("[data-invoice]")
          .forEach(
            (b) =>
              (b.onclick = () =>
                invoiceDetails(
                  pane.querySelector("[data-invoice-detail]"),
                  acc,
                  b.dataset.invoice,
                )),
          );
        return;
      }
      const data = await read(acc, "firewalls");
      if (!pane.isConnected || seq !== generation) return;
      pane.innerHTML = `<div class="row-actions"><h3>${escape(acc.name)} 的云防火墙</h3><button class="primary" data-new-firewall>创建防火墙</button></div><p class="help">修改共享规则会影响所有绑定。服务器绑定请从其详情进入；不会修改账户全局默认。</p>${data.items.length ? data.items.map((f) => `<article class="firewall-card"><h3>${escape(f.label)} · ${f.status === "enabled" ? "已启用" : "已禁用"} · ID ${f.id}</h3><p>${f.devices.length}个绑定：${escape(f.devices.map((d) => `${d.entity.label || d.entity.id}（${d.entity.type}）`).join("、") || "未绑定")}</p><dl class="summary"><dt>入站默认</dt><dd>${f.rules.inbound_policy === "ACCEPT" ? "允许" : "拒绝"}</dd><dt>出站默认</dt><dd>${f.rules.outbound_policy === "ACCEPT" ? "允许" : "拒绝"}</dd></dl>${ruleTable(f.rules)}<div class="row-actions"><button data-rule-edit="${f.id}">修改规则</button><button data-status-edit="${f.id}">${f.status === "enabled" ? "禁用" : "启用"}</button><button class="danger" data-delete-fw="${f.id}" ${f.devices.length ? "disabled" : ""}>删除</button></div>${f.devices.length ? '<p class="help">仍有绑定，不允许直接删除；先从所选服务器详情显式解绑。</p>' : ""}</article>`).join("") : "<p>暂无云防火墙</p>"}`;
      pane.querySelector("[data-new-firewall]").onclick = () =>
        firewallDialog(acc, null, "firewall-create");
      for (const [attr, action] of [
        ["data-rule-edit", "firewall-rules"],
        ["data-status-edit", "firewall-status"],
        ["data-delete-fw", "firewall-delete"],
      ])
        pane.querySelectorAll(`[${attr}]`).forEach(
          (b) =>
            (b.onclick = () =>
              firewallDialog(
                acc,
                data.items.find((f) => f.id === Number(b.getAttribute(attr))),
                action,
              )),
        );
    } catch (e) {
      if (pane.isConnected && seq === generation) {
        pane.innerHTML = `<p class="error-box">${escape(e.message)}</p><p class="help">账单需要account读取权限；防火墙需要firewall读取权限。缺少这些权限不影响无关服务器管理。</p><button data-retry>重试</button>`;
        pane.querySelector("[data-retry]").onclick = () => load(page);
      }
    }
  };
  await load();
}
const invoiceRequests = new WeakMap();
async function invoiceDetails(pane, acc, id, page = 1) {
  const request = {};
  invoiceRequests.set(pane, request);
  pane.innerHTML = '<p class="loading">正在读取账单明细…</p>';
  try {
    const d = await read(acc, "invoice", { target: id, page: String(page) });
    if (!pane.isConnected || invoiceRequests.get(pane) !== request) return;
    pane.innerHTML = `<h3>账单 ${escape(d.invoice.label)} · ${val(d.invoice.total, "USD")}</h3><p class="help">${escape(d.statusNote)}</p><div class="table-wrap"><table><thead><tr><th>项目 / 周期</th><th>数量</th><th>单价</th><th>金额</th></tr></thead><tbody>${d.items.data.map((i) => `<tr><td>${escape(i.label)}<br>${escape(i.from)} — ${escape(i.to)}</td><td>${val(i.quantity)}</td><td>${escape(i.unit_price)} USD</td><td>${val(i.amount, "USD")}</td></tr>`).join("")}</tbody></table></div><div class="pagination"><button data-prev ${page <= 1 ? "disabled" : ""}>上一页明细</button><span>第${page}/${d.items.pages}页</span><button data-next ${page >= d.items.pages ? "disabled" : ""}>下一页明细</button></div>`;
    pane.querySelector("[data-prev]").onclick = () =>
      invoiceDetails(pane, acc, id, page - 1);
    pane.querySelector("[data-next]").onclick = () =>
      invoiceDetails(pane, acc, id, page + 1);
  } catch (e) {
    if (pane.isConnected && invoiceRequests.get(pane) === request)
      pane.innerHTML = `<p class="error-box">${escape(e.message)}</p>`;
  }
}
function ruleTable(rules) {
  return `<div class="table-wrap"><table><thead><tr><th>方向 / 顺序</th><th>操作 / 协议</th><th>端口</th><th>IPv4 / IPv6 来源或目标</th></tr></thead><tbody>${["inbound", "outbound"].flatMap((dir) => rules[dir].map((r, i) => `<tr><td>${dir === "inbound" ? "入站" : "出站"} ${i + 1}</td><td>${r.action === "ACCEPT" ? "允许" : "拒绝"} / ${escape(r.protocol)}</td><td>${escape(r.ports || "全部或不适用")}</td><td>${escape([...(r.addresses.ipv4 || []), ...(r.addresses.ipv6 || [])].join("、"))}</td></tr>`)).join("")}</tbody></table></div>`;
}
function firewallDialog(acc, f, action) {
  const id = ctx.intentID();
  if (!id) return;
  const name = f?.label || "";
  ctx.openDialog(
    {
      "firewall-create": "创建防火墙",
      "firewall-rules": "修改防火墙规则",
      "firewall-status": "切换防火墙状态",
      "firewall-delete": "删除防火墙",
    }[action],
    `<form class="firewall-form"><div class="error-box" role="alert"></div><p>Linode · ${escape(acc.name)}</p>${f ? `<p class="notice">本操作影响 ${f.devices.length} 个绑定：${escape(f.devices.map((d) => d.entity.label || String(d.entity.id)).join("、") || "无绑定")}。请确认所有受影响服务器；规则修改可能中断连接。</p>` : '<label class="field">防火墙名称<input name="label" minlength="3" maxlength="32" required></label>'}${["firewall-create", "firewall-rules"].includes(action) ? rulesEditor() : ""}<label class="check"><input name="acknowledge" type="checkbox" required>已了解并确认影响范围</label><label class="field">输入防火墙名称确认<input name="confirmName" required autocomplete="off"></label><div class="dialog-actions"><button type="button" data-close>取消</button><button class="danger" type="submit">核实并确认</button></div></form>`,
  );
  const form = ctx.dialog.querySelector("form");
  if (form.querySelector("[data-add-rule]"))
    form.querySelector("[data-add-rule]").onclick = () => addRule(form);
  if (f && action === "firewall-rules") {
    form.elements.inPolicy.value = f.rules.inbound_policy;
    form.elements.outPolicy.value = f.rules.outbound_policy;
    for (const dir of ["inbound", "outbound"])
      for (const r of f.rules[dir]) addRule(form, { ...r, direction: dir });
  }
  form.onsubmit = (e) => {
    e.preventDefault();
    ctx.formAction(form, async () => {
      const chosen = f?.label || form.elements.label.value;
      const advanced = {
        snapshot: f?.snapshot || "",
        acknowledge: form.elements.acknowledge.checked,
        ...(action === "firewall-create" ? { label: chosen } : {}),
        ...(["firewall-create", "firewall-rules"].includes(action)
          ? { rules: readRules(form) }
          : {}),
        ...(action === "firewall-status"
          ? { status: f.status === "enabled" ? "disabled" : "enabled" }
          : {}),
      };
      await ctx.submit({
        id,
        accountId: acc.id,
        action,
        region: "",
        target: f ? String(f.id) : "",
        name: chosen,
        spec: {},
        advanced,
        confirmation: "",
        confirmName: form.elements.confirmName.value,
      });
      form.reset();
      ctx.dialog.close();
    });
  };
}
