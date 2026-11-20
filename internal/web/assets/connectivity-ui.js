const workflowStates = {
  queued: "等待筛选",
  creating: "正在创建",
  "waiting-ready": "等待服务就绪",
  probing: "正在检测",
  replacing: "正在更换 IP",
  accepted: "符合检测条件",
  paused: "已暂停",
  unknown: "云端结果待核实",
  failed: "失败",
  exhausted: "已达到上限",
  stopped: "已停止",
};
const providers = {
  aws: "AWS",
  azure: "Azure",
  linode: "Linode",
  digitalocean: "DigitalOcean",
};
const resourceKinds = {
  instance: "实例",
  vm: "虚拟机",
  "public-ip": "公网 IP",
  disk: "磁盘",
  nic: "网卡",
  nsg: "网络安全组",
};
const resourceStates = {
  present: "仍存在",
  deleted: "已确认删除",
  unverified: "存在及费用待核实",
  unknown: "状态未知",
  "deleted-confirmed": "已确认删除",
};
const writeActions = {
  "cloud-write-dispatch": "云操作派发",
  "create-instance": "创建实例",
  "delete-instance": "删除实例",
  "stop-instance": "停止实例",
  "start-instance": "启动实例",
  "create-public-ip": "创建公网 IP",
  "associate-public-ip": "绑定新公网 IP",
  "delete-public-ip": "删除旧公网 IP",
};
const writeStates = {
  dispatching: "已记录派发，云端结果待核实",
  confirmed: "云端已确认",
  unknown: "结果未知，不自动重发",
  failed: "云端拒绝或执行失败",
};
const methods = {
  "aws-stop-start": "停止 / 启动",
  "azure-public-ip": "更换公网 IP",
  recreate: "删除本轮实例后重建",
  create: "新建实例",
};
const terminal = new Set(["accepted", "stopped", "failed", "exhausted"]);
const checkErrors = {
  timeout: "检测超时",
  refused: "端口拒绝连接",
  "connection-refused": "端口拒绝连接",
  "tls-error": "TLS 握手未通过",
  "http-status": "HTTP 状态不符",
  "challenge-mismatch": "新实例挑战值不符",
  "egress-mismatch": "实际出口不符",
  offline: "探针离线",
  success: "检查通过",
  ok: "检查通过",
  connection_refused: "端口拒绝连接",
  network_error: "网络连接失败",
  tls_error: "TLS 握手未通过",
  http_status: "HTTP 状态不符",
  challenge_mismatch: "新实例挑战值不符",
  invalid_target: "检测目标无效",
  egress_unverified: "实际出口待核实",
  response_too_large: "目标响应过大",
  http_error: "应用检查失败",
  connection_error: "连接失败",
  redirect_rejected: "目标返回重定向，已拒绝跟随",
  canceled: "检测已取消",
};
const list = (v) => (Array.isArray(v) ? v : []);
function date(v) {
  if (!v) return "尚未记录";
  const d = new Date(typeof v === "number" ? v * 1000 : v);
  return Number.isNaN(d.getTime()) ? "时间未确认" : d.toLocaleString();
}
function status(s, esc) {
  return `<span class="status ${s === "accepted" ? "good" : ["unknown", "paused", "exhausted"].includes(s) ? "warn" : s === "failed" ? "bad" : ""}">${esc(workflowStates[s] || s || "尚未开始")}</span>`;
}
const money = (v) =>
  Number.isFinite(Number(v)) ? "$" + Number(v).toFixed(2) : "未确认";

export function screeningFields() {
  return `<section class="screening-create"><h3>国内 IP 可用性检测</h3><label class="check"><input type="checkbox" name="screeningEnabled">创建后自动检测公网 IPv4</label><p class="help">关闭时按原有方式创建。检测需要目标端口实际监听；不会覆盖你的初始化脚本，也不会自动安装健康服务。</p><fieldset data-screening-fields hidden disabled><legend class="sr-only">国内检测设置</legend><div class="grid2"><label class="field">国内探针<select name="screeningProbe" required><option value="">正在读取探针…</option></select></label><label class="field">海外对照<select name="screeningControl" required><option value="manager">Narbe 管理端（需核实海外出口）</option></select></label></div><p class="help" data-screening-probe-status>尚未选择国内探针</p><div class="grid2"><label class="field">检测协议<select name="screeningProtocol"><option value="tcp">TCP Connect</option><option value="tls">TLS 握手</option><option value="http">HTTP 应用检查</option><option value="https">HTTPS 应用检查</option></select></label><label class="field">检测端口<input name="screeningPort" type="number" min="1" max="65535" value="22" required></label></div><p class="notice">请确认系统服务监听此端口，云防火墙及系统防火墙均允许访问。尚无有效检测目标或海外也不通时，只等待或暂停，不据此删除实例。</p><div data-screening-host hidden><label class="field">TLS 服务器名（可选）<input name="screeningServerName" maxlength="253" placeholder="服务证书中的域名"></label></div><div data-screening-http hidden><div class="grid2"><label class="field">HTTP 路径<input name="screeningPath" value="/" maxlength="1024" placeholder="/health"></label><label class="field">预期状态码<input name="screeningStatus" type="number" min="200" max="299" value="200"></label></div><label class="field">一次性挑战值（可选）<input name="screeningChallenge" maxlength="128" autocomplete="off"><p class="help">如使用，请让新实例的健康端点响应此值。Narbe 不会替你修改脚本；内容不会保存到浏览器持久存储。</p></label></div><details><summary>检测次数与等待时间</summary><div class="grid2"><label class="field">单次超时（秒）<input name="screeningTimeout" type="number" min="1" max="15" value="5" required></label><label class="field">检测间隔（秒）<input name="screeningInterval" type="number" min="5" max="300" value="10" required></label><label class="field">国内连续成功次数<input name="screeningSuccesses" type="number" min="2" max="10" value="3" required></label><label class="field">疑似不可达连续失败次数<input name="screeningFailures" type="number" min="2" max="10" value="3" required></label><label class="field">每轮就绪等待（秒）<input name="screeningReady" type="number" min="30" max="1800" value="300" required></label><label class="field">整轮最长时间（秒）<input name="screeningDuration" type="number" min="60" max="21600" value="1800" required></label></div></details><label class="check"><input name="screeningReplace" type="checkbox">有充分失败证据时自动更换 IP</label><div data-screening-replacement hidden><div class="grid2"><label class="field">最多筛选轮次<input name="screeningAttempts" type="number" min="1" max="20" value="5" required></label><label class="field">本轮估算预算（USD）<input name="screeningBudget" type="number" min="0.01" max="10000" step="0.01" value="5" required></label><label class="field">每轮保守费用估算（USD）<input name="screeningCost" type="number" min="0.01" max="10000" step="0.01" value="1" required></label></div><p class="notice">只操作本次任务新建并核实归属的资源。换 IP 可能停机，优先使用原机地址能力；Linode 配额不足或普通 Droplet 无法原地更换时，可能删除本轮实例再建。新增服务地址不代表原始公网 IP 已变化。估算不是云厂商账单，独立磁盘、公网 IP 或延迟出账可能另收费。停止筛选不会自动删除已产生的资源。</p><label class="check"><input name="screeningRisk" type="checkbox">我同意本轮按上述次数和估算预算自动创建、停机或删除本轮资源，并承担实际费用</label></div></fieldset></section>`;
}

export function bindScreening(form, { api }) {
  const fields = form.elements,
    box = form.querySelector("[data-screening-fields]");
  let probes = [],
    failure = "",
    loaded = false,
    loading = false;
  const sync = () => {
    const enabled = fields.screeningEnabled.checked;
    box.hidden = !enabled;
    box.disabled = !enabled;
    const protocol = fields.screeningProtocol.value,
      replace = fields.screeningReplace.checked;
    form.querySelector("[data-screening-http]").hidden = ![
      "http",
      "https",
    ].includes(protocol);
    form.querySelector("[data-screening-host]").hidden = ![
      "tls",
      "http",
      "https",
    ].includes(protocol);
    form.querySelector("[data-screening-host] label").firstChild.textContent =
      protocol === "http" ? "HTTP Host（可选）" : "TLS 服务器名（可选）";
    form.querySelector("[data-screening-replacement]").hidden = !replace;
    fields.screeningRisk.required = enabled && replace;
    const p = probes.find((p) => p.id === fields.screeningProbe.value);
    form.querySelector("[data-screening-probe-status]").textContent =
      failure ||
      (p
        ? `${p.name} · ${p.status === "online" ? "在线" : "离线或出口未确认"} · 实际出口 ${p.egress?.ip || "未确认"} / ${p.egress?.country || "地区未确认"}；每次结果仍需核实出口。`
        : "尚未选择国内探针。先到「国内连通性」登记并启动探针。");
  };
  for (const key of [
    "screeningEnabled",
    "screeningProtocol",
    "screeningReplace",
    "screeningProbe",
  ])
    fields[key].addEventListener("change", sync);
  sync();
  function loadProbes() {
    if (!fields.screeningEnabled.checked || loaded || loading) return;
    loading = true;
    failure = "";
    api("probes")
      .then((v) => {
        if (!form.isConnected) return;
        if (!Array.isArray(v.items))
          throw Error("探针列表格式无效，请到国内连通性页面核实");
        probes = v.items;
        loaded = true;
        const option = (p) => {
          const o = document.createElement("option");
          o.value = p.id;
          o.textContent = `${p.name} · ${p.status === "online" ? "在线" : "离线 / 出口未确认"}`;
          return o;
        };
        fields.screeningProbe.replaceChildren(
          new Option("选择国内探针", ""),
          ...probes
            .filter((p) => p.enabled && p.role === "domestic")
            .map(option),
        );
        fields.screeningControl.replaceChildren(
          new Option("Narbe 管理端（需核实海外出口）", "manager"),
          ...probes
            .filter((p) => p.enabled && p.role === "control")
            .map(option),
        );
        sync();
      })
      .catch((e) => {
        failure = "探针读取失败：" + e.message;
        sync();
      })
      .finally(() => {
        loading = false;
      });
  }
  fields.screeningEnabled.addEventListener("change", loadProbes);
  return {
    spec() {
      if (!fields.screeningEnabled.checked) return undefined;
      if (failure) throw Error(failure);
      const p = probes.find((p) => p.id === fields.screeningProbe.value);
      if (!p || !p.enabled || p.role !== "domestic")
        throw Error("请选择已启用的国内探针");
      if (fields.screeningReplace.checked && !fields.screeningRisk.checked)
        throw Error("请先确认本轮自动换 IP 的费用和删除风险");
      const protocol = fields.screeningProtocol.value;
      return {
        probeId: p.id,
        controlProbeId: fields.screeningControl.value,
        protocol,
        port: Number(fields.screeningPort.value),
        serverName: ["tls", "http", "https"].includes(protocol)
          ? fields.screeningServerName.value.trim()
          : "",
        path: ["http", "https"].includes(protocol)
          ? fields.screeningPath.value
          : "",
        expectedStatus: ["http", "https"].includes(protocol)
          ? Number(fields.screeningStatus.value)
          : 0,
        challenge: ["http", "https"].includes(protocol)
          ? fields.screeningChallenge.value
          : "",
        timeoutSeconds: Number(fields.screeningTimeout.value),
        intervalSeconds: Number(fields.screeningInterval.value),
        successes: Number(fields.screeningSuccesses.value),
        failures: Number(fields.screeningFailures.value),
        readySeconds: Number(fields.screeningReady.value),
        maxDurationSeconds: Number(fields.screeningDuration.value),
        maxAttempts: fields.screeningReplace.checked
          ? Number(fields.screeningAttempts.value)
          : 1,
        autoReplace: fields.screeningReplace.checked,
        budgetUSD: Number(fields.screeningBudget.value),
        costPerAttemptUSD: Number(fields.screeningCost.value),
        riskAccepted:
          fields.screeningReplace.checked && fields.screeningRisk.checked,
      };
    },
  };
}

function resultSummary(r, esc) {
  if (!r) return "尚无有效结果";
  return esc(
    (r.success ? "成功" : "未成功") +
      ` · ${r.latencyMs ?? 0} ms` +
      (r.classification
        ? " · " + (checkErrors[r.classification] || r.classification)
        : "") +
      (r.error ? " · " + r.error : ""),
  );
}
function roundLedger(round, esc) {
  const operation = round.operation || {},
    resources = new Map(),
    writes = [];
  for (const r of list(operation.ledger?.resources)) {
    if (!r || typeof r.id !== "string" || !r.id) continue;
    resources.set(r.id, {
      id: r.id,
      kind: typeof r.kind === "string" ? r.kind : "",
      state: typeof r.state === "string" ? r.state : "unverified",
    });
  }
  for (const id of list(operation.owned)) {
    if (typeof id === "string" && id && !resources.has(id))
      resources.set(id, { id, kind: "", state: "unverified" });
  }
  const seen = new Set();
  for (const write of [
    ...list(round.writes),
    ...list(operation.screening?.writes),
  ]) {
    if (
      !write ||
      typeof write.action !== "string" ||
      typeof write.resourceId !== "string" ||
      typeof write.state !== "string"
    )
      continue;
    const key = JSON.stringify([
      write.action,
      write.resourceId,
      write.state,
      write.at,
    ]);
    if (!seen.has(key)) {
      writes.push(write);
      seen.add(key);
    }
  }
  return `<details class="screening-operation-ledger"><summary>本轮云操作与关联资源</summary>${typeof round.createToken === "string" && round.createToken ? `<p class="help">云控制台核实用的本轮创建标识：<span class="mono">${esc(round.createToken)}</span>。AWS / Azure 标签为 narbe-job；Linode / DigitalOcean 标签为 narbe- 加此标识。</p>` : ""}<h4>云操作记录</h4>${writes.length ? `<ul class="screening-resources">${writes.map((w) => `<li>${esc(writeActions[w.action] || w.action)} · <span class="mono">${esc(w.resourceId || "资源 ID 未确认")}</span><br>${esc(writeStates[w.state] || w.state)} · ${esc(date(w.at))}</li>`).join("")}</ul>` : '<p class="help">尚无可确认的云写记录，不代表云端未发生操作。</p>'}<h4>本轮关联资源</h4>${resources.size ? `<ul class="screening-resources">${[...resources.values()].map((r) => `<li data-round-resource="${esc(r.id)}"><span class="mono">${esc(r.id)}</span> · ${esc(resourceKinds[r.kind] || r.kind || "关联资源")} · ${esc(resourceStates[r.state] || r.state || "存在及费用待核实")}</li>`).join("")}</ul>` : '<p class="help">关联资源未知，需要到云控制台核实。</p>'}<p class="help">历史资源记录会保留。仅标记已确认删除的资源才有删除证据；存在或费用未知的资源仍需到云控制台核实。</p></details>`;
}
function workflowCard(w, esc) {
  const rounds = list(w.rounds);
  return `<article class="screening-task" data-screening-id="${esc(w.id)}"><div class="task-top"><div><h3>${esc(providers[w.provider] || w.provider)} · ${esc(w.region)} · 第 ${esc(w.attempt || 1)} / ${esc(w.maxAttempts || "—")} 次</h3><p class="help">更新于 ${esc(date(w.updated))}</p></div>${status(w.state, esc)}</div><p class="screening-stage">${esc(w.stage || workflowStates[w.state] || "等待执行")}</p>${w.reason ? `<p class="notice">${esc(w.reason)}</p>` : ""}<dl class="summary"><dt>当前实例</dt><dd class="mono">${esc(w.resourceId || "尚未取得")}</dd><dt>公网 IPv4</dt><dd class="mono">${esc(w.ip || "等待分配")}</dd><dt>估算费用 / 预算</dt><dd>${money(w.estimatedUSD)} / ${money(w.budgetUSD)}（实际账单以云厂商为准）</dd><dt>更换方式</dt><dd>${esc(methods[w.method] || w.method || "尚未更换")}</dd><dt>国内 / 海外对照</dt><dd>${resultSummary(rounds.at(-1)?.domestic, esc)}<br>${resultSummary(rounds.at(-1)?.control, esc)}</dd></dl>${["stopped", "exhausted", "paused", "unknown", "failed"].includes(w.state) ? '<p class="notice">已产生的实例或附属资源可能仍存在并继续计费。停止筛选不会自动删除资源，请核实下面的资源记录。</p>' : ""}<details class="screening-history"><summary>每轮检测与云资源记录（${rounds.length} 轮）</summary>${rounds.length ? rounds.map((r) => `<section class="screening-round"><h4>第 ${esc(r.attempt)} 次 · ${esc(workflowStates[r.state] || r.state || "未完成")}</h4><p class="mono">${esc(r.resourceId || "资源未确认")} · ${esc(r.ip || "IP 未确认")}</p><p>国内：${resultSummary(r.domestic, esc)}<br>海外：${resultSummary(r.control, esc)}</p><p class="help">${esc(methods[r.method] || r.method || "创建")} · 估算 ${money(r.estimatedUSD)}${r.repeatedIP ? " · 再次分配到相同 IP，已记录" : ""}</p>${roundLedger(r, esc)}</section>`).join("") : '<p class="help">尚无检测轮次。</p>'}<h4>已产生的资源</h4>${list(w.resources).length ? `<ul class="screening-resources">${w.resources.map((r) => `<li><span class="mono">${esc(typeof r === "string" ? r : r.id || r.resourceId || "ID 未确认")}</span>${typeof r === "object" ? ` · ${esc(resourceKinds[r.kind] || r.kind || "资源")} · ${esc(resourceStates[r.state] || r.state || "存在及收费状态待核实")}` : " · 存在及收费状态待核实"}</li>`).join("")}</ul>` : '<p class="help">尚无可确认的附属资源；资源未知不代表未计费。请到云控制台核实。</p>'}<p class="help mono">筛选任务 ${esc(w.id)} · 创建任务 ${esc(w.jobId || "未确认")}</p></details><div class="row-actions">${!terminal.has(w.state) ? `<button class="danger" data-screening-stop="${esc(w.id)}">停止自动筛选</button>` : ""}<button data-screening-reconcile="${esc(w.id)}">只读核实</button></div><p class="help">只读核实不重发未知云写，也不会重新开始自动筛选。</p></article>`;
}

export async function connectivityPage(el, { api, esc, formAction, toast }) {
  el.innerHTML = `<div class="page-head"><div><h1>国内连通性</h1><p class="sub">检查国内与海外对同一公网 IP、同一端口的访问结果。</p></div><button data-connectivity-refresh>↻ 刷新</button></div><div class="error-box" data-connectivity-error role="alert"></div><section class="panel connectivity-workflows"><div class="panel-head"><h2>自动筛选任务</h2></div><div data-screening-list aria-live="polite"><p class="loading">正在读取任务…</p></div></section><section class="panel connectivity-probes"><div class="panel-head"><h2>检测探针</h2></div><p class="help">探针主动通过 HTTPS 连接管理端，无需开放入站端口。物理位置不能证明国内出口；Clash、TUN 或 VPN 可能改变实际出口。</p><div data-probe-list><p class="loading">正在读取探针…</p></div><details class="probe-register"><summary>添加检测探针</summary><form id="probe-registration"><div class="error-box" role="alert"></div><label class="field">探针备注<input name="probeName" maxlength="80" required placeholder="例如：上海家庭网络"></label><div class="grid2"><label class="field">用途<select name="probeRole"><option value="domestic">中国大陆检测</option><option value="control">海外对照检测</option></select></label><label class="field">预期出口公网 IPv4<input name="probeIP" maxlength="45" required placeholder="探针容器的实际出口 IP" autocomplete="off"></label></div><p class="help">填写容器真实出口，用于防止网络出口变化后产生误判。出口地区仍需核验；不能凭备注认定为国内直连。</p><button type="submit" class="primary">生成一次性注册凭据</button></form><div data-probe-registration-result aria-live="polite"></div></details></section>`;
  const error = el.querySelector("[data-connectivity-error]");
  let busy = false;
  async function load() {
    if (busy || !el.isConnected) return;
    busy = true;
    error.textContent = "";
    try {
      const [work, probes] = await Promise.all([
        api("screenings"),
        api("probes"),
      ]);
      if (!el.isConnected) return;
      if (!Array.isArray(work.items) || !Array.isArray(probes.items))
        throw Error("检测数据格式无效，请重试；系统不会据此继续更换 IP");
      el.querySelector("[data-screening-list]").innerHTML = work.items.length
        ? work.items.map((w) => workflowCard(w, esc)).join("")
        : '<div class="empty"><h3>还没有筛选任务</h3><p>在「创建服务器」中开启国内 IP 可用性检测。已有服务器不会被自动纳入或删除。</p></div>';
      el.querySelector("[data-probe-list]").innerHTML = probes.items.length
        ? probes.items
            .map(
              (p) =>
                `<article class="probe-row"><div class="task-top"><div><h3>${esc(p.name)} <span class="status ${p.enabled && p.status === "online" ? "good" : "warn"}">${p.enabled ? (p.status === "online" ? "在线" : "离线 / 出口待核实") : "已停用"}</span></h3><p>${p.role === "domestic" ? "中国大陆检测" : "海外对照"} · 实际出口 <span class="mono">${esc(p.egress?.ip || "尚未核实")}</span> · ${esc(p.egress?.country || "地区未确认")}</p><p class="help">预期出口 ${esc(p.expectedIP || "未配置")} · 最近心跳 ${esc(date(p.lastSeen))}<br>最近检测 ${esc(date(p.lastCheckAt))} · 版本 ${esc(p.version || "尚未注册")} · 检测 ${Number(p.checks || 0)} 次 · 最近 ${Number(p.recentChecks ?? p.checks ?? 0)} 次成功率 ${(p.recentChecks ?? p.checks) ? Math.round((Number(p.recentSuccesses ?? p.successes ?? 0) / Number(p.recentChecks ?? p.checks)) * 100) + "%" : "暂无结果"}</p>${p.egress?.error ? `<p class="notice">${esc(p.egress.error)}</p>` : ""}</div><div class="row-actions">${p.enabled ? `<button data-probe-disable="${esc(p.id)}">停用</button>` : ""}</div></div></article>`,
            )
            .join("")
        : '<p class="empty">尚无探针。先添加探针，再在国内网络中的 Docker 启动。</p>';
      el.querySelectorAll("[data-screening-id]").forEach((card, index) => {
        const kind = work.items[index]?.kind;
        if (kind === "check" || kind === "manual-ip") {
          card
            .querySelector("h3")
            .prepend(kind === "check" ? "手动检测 · " : "换 IP 后检测 · ");
          card.querySelector("[data-screening-stop]")?.remove();
        }
      });
      el.querySelectorAll(".probe-row").forEach((row, index) => {
        const p = probes.items[index];
        if (!p?.id) return;
        const button = document.createElement("button");
        button.type = "button";
        button.className = "danger";
        button.dataset.probeDelete = p.id;
        button.textContent = "删除探针";
        row.querySelector(".row-actions").append(button);
      });
      for (const [attr, path, method, msg] of [
        [
          "screening-stop",
          "screenings",
          "POST",
          "已停止自动筛选；请核实仍存在的资源",
        ],
        [
          "screening-reconcile",
          "screenings",
          "POST",
          "已完成只读核实，不重发云写",
        ],
        ["probe-disable", "probes", "POST", "探针已停用"],
        ["probe-delete", "probes", "DELETE", "探针已删除"],
      ]) {
        el.querySelectorAll(`[data-${attr}]`).forEach(
          (b) =>
            (b.onclick = async () => {
              b.disabled = true;
              try {
                const id = b.getAttribute("data-" + attr);
                const suffix = {
                  "screening-stop": "/stop",
                  "screening-reconcile": "/reconcile",
                  "probe-disable": "/disable",
                  "probe-delete": "",
                }[attr];
                await api(`${path}/${encodeURIComponent(id)}${suffix}`, {
                  method,
                  ...(method === "DELETE" ? {} : { body: {} }),
                });
                toast(msg);
                await load();
              } catch (e) {
                error.textContent = e.message;
                b.disabled = false;
              }
            }),
        );
      }
    } catch (e) {
      if (el.isConnected) {
        error.textContent =
          e.message + "；请刷新重试，已有记录可能不是最新状态。";
        for (const selector of ["[data-screening-list]", "[data-probe-list]"]) {
          const target = el.querySelector(selector);
          if (target.querySelector(".loading"))
            target.innerHTML =
              '<p class="help">读取未完成，请使用上方刷新按钮重试。</p>';
        }
      }
    } finally {
      busy = false;
    }
  }
  el.querySelector("[data-connectivity-refresh]").onclick = load;
  const form = el.querySelector("#probe-registration");
  form.onsubmit = (e) => {
    e.preventDefault();
    formAction(form, async () => {
      const v = await api("probes", {
        method: "POST",
        body: {
          name: form.elements.probeName.value,
          role: form.elements.probeRole.value,
          expectedIP: form.elements.probeIP.value.trim(),
        },
      });
      if (
        typeof v.registrationToken !== "string" ||
        !v.registrationToken ||
        typeof v.id !== "string"
      )
        throw Error("未取得有效的注册凭据，请核实探针记录");
      const area = el.querySelector("[data-probe-registration-result]");
      area.innerHTML = `<div class="notice">注册凭据只展示这一次，5 分钟后失效。仅交给这台探针，不包含云账号密钥；离开页面即清除。</div><p class="help">使用正式测试版 <span class="mono">v0.1.0-beta.6</span> 探针镜像，安装包已固定不可变 Digest。管理端必须可通过可信 HTTPS 访问。</p><label class="field">一次性注册凭据<input readonly data-probe-token autocomplete="off"></label><p class="help">从 GitHub 的 Narbe v0.1.0-beta.6 Release 下载并解压正式安装包，在探针机器的解压目录运行下列命令，按提示输入管理端 HTTPS 地址及上方一次性凭据。不要把云厂商 Token 填入探针。</p><pre class="probe-deploy-command">./install-probe.sh

./install-probe.sh status
./install-probe.sh logs</pre><p class="help">安装器拉取已发布镜像，不在探针机器编译源码。已有配置和身份保留；在线及实际出口以后台状态为准。</p><button type="button" data-probe-clear>清除页面中的凭据</button>`;
      area.querySelector("[data-probe-token]").value = v.registrationToken;
      area.querySelector("[data-probe-clear]").onclick = () =>
        area.replaceChildren();
      form.reset();
      await load();
    });
  };
  await load();
  const timer = setInterval(() => {
    if (!el.isConnected) {
      clearInterval(timer);
      return;
    }
    if (!document.hidden) load();
  }, 5000);
}
