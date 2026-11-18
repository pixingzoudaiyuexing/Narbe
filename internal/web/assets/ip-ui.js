const list = (value) => (Array.isArray(value) ? value : []);
const time = (value) => {
  if (!value) return "尚未检测";
  const parsed = new Date(typeof value === "number" ? value * 1000 : value);
  return Number.isNaN(parsed.getTime())
    ? "时间未确认"
    : parsed.toLocaleString("zh-CN", { hour12: false });
};
const addressKind = {
  primary: "原始公网 IP",
  secondary: "额外 IPv4",
  reserved: "Reserved IP",
  elastic: "Elastic IP",
  public: "公网 IP",
};
const readableAddress = (a) =>
  a.kind === "reserved" ? "Reserved IP" : addressKind[a.kind] || "额外 IPv4";

function checkConfig(form) {
  const f = form.elements;
  const protocol = f.protocol.value;
  return {
    probeId: f.probeId.value,
    controlProbeId: f.controlProbeId.value,
    protocol,
    port: Number(f.port.value),
    serverName: ["tls", "http", "https"].includes(protocol)
      ? f.serverName.value.trim()
      : "",
    path: ["http", "https"].includes(protocol) ? f.path.value : "",
    expectedStatus: ["http", "https"].includes(protocol)
      ? Number(f.expectedStatus.value)
      : 0,
    challenge: "",
    timeoutSeconds: Number(f.timeoutSeconds.value),
    intervalSeconds: Number(f.intervalSeconds.value),
    successes: Number(f.successes.value),
    failures: Number(f.failures.value),
    readySeconds: 300,
    maxDurationSeconds: 1800,
    maxAttempts: 1,
    autoReplace: false,
    budgetUSD: 1,
    costPerAttemptUSD: 1,
    riskAccepted: false,
  };
}

function checkFields() {
  return `<form id="ip-check-form"><div class="error-box" role="alert"></div><div class="grid2"><label class="field">国内探针<select name="probeId" required><option value="">选择国内探针</option></select></label><label class="field">海外对照<select name="controlProbeId" required><option value="manager">Narbe 管理端</option></select></label><label class="field">检测地址<select name="ip" required></select></label><label class="field">协议<select name="protocol"><option value="tcp">TCP</option><option value="tls">TLS</option><option value="http">HTTP</option><option value="https">HTTPS</option></select></label><label class="field">端口<input name="port" type="number" min="1" max="65535" value="22" required></label><label class="field" data-ip-server-name hidden>服务域名 / SNI / HTTP Host<input name="serverName" maxlength="253" placeholder="例如 service.example.com"></label></div><div class="grid2" data-ip-http hidden><label class="field">HTTP 路径<input name="path" maxlength="1024" value="/"></label><label class="field">预期状态码<input name="expectedStatus" type="number" min="200" max="299" value="200"></label></div><div class="grid2"><label class="field">单次超时（秒）<input name="timeoutSeconds" type="number" min="1" max="15" value="5" required></label><label class="field">检测间隔（秒）<input name="intervalSeconds" type="number" min="2" max="300" value="10" required></label><label class="field">连续成功次数<input name="successes" type="number" min="2" max="10" value="3" required></label><label class="field">连续失败次数<input name="failures" type="number" min="2" max="10" value="3" required></label></div><p class="help" data-ip-probe-status></p><div class="dialog-actions"><button class="primary" type="submit">开始检测 IP</button></div></form>`;
}

function lastCheck(check, esc) {
  if (!check || typeof check !== "object")
    return '<p class="help">尚无此服务器的国内检测记录。</p>';
  const round = list(check.rounds).at(-1);
  const result = round?.domestic || check.domestic;
  const control = round?.control || check.control;
  const stamp = result?.at || check.updated || check.created;
  const latency = Number.isFinite(Number(result?.latencyMs))
    ? ` · ${esc(result.latencyMs)} ms`
    : "";
  return `<dl class="summary"><dt>最近结果</dt><dd>${esc(check.stage || check.reason || check.state || "结果待核实")}</dd><dt>检测时间</dt><dd>${esc(time(stamp))}</dd><dt>国内延迟</dt><dd>${result ? `${esc(result.success ? "检查通过" : "未通过")}${latency}` : "尚无结果"}</dd><dt>海外对照</dt><dd>${control ? esc(control.success ? "可达" : "未通过") : "尚无结果"}</dd></dl>`;
}

function resultNotice(info, esc) {
  return list(info.warnings)
    .filter((w) => typeof w === "string" && w)
    .map((w) => `<p class="notice">${esc(w)}</p>`)
    .join("");
}

export function ipJobResult(job, esc) {
  const ip = job.result?.ip;
  if (!ip || typeof ip !== "object") return "";
  const before = ip.before || {};
  const after = ip.after || {};
  const oldIP = ip.oldIP || before.primaryIPv4 || "未确认";
  const newIP = ip.newIP || after.primaryIPv4 || "未确认";
  const primary = ip.primaryChanged
    ? "原始公网 IP 已变化"
    : "原始公网 IP 未变化；新增地址不会自动成为原始公网 IP";
  return `<div class="ip-job-result"><dl class="summary"><dt>原始 IP</dt><dd class="mono">${esc(oldIP)}</dd><dt>新地址</dt><dd class="mono">${esc(newIP)}</dd><dt>实际结果</dt><dd>${esc(primary)}</dd>${list(after.serviceIPv4).length ? `<dt>对外服务 IP</dt><dd class="mono">${list(after.serviceIPv4).map(esc).join(" / ")}</dd>` : ""}</dl>${ip.addressRepeated || (oldIP === newIP && newIP !== "未确认") ? '<p class="notice">云端再次分配了相同 IP，没有取得不同的新地址。</p>' : ""}${ip.notice ? `<p class="notice">${esc(ip.notice)}</p>` : ""}${ip.guestNetworkingRequired ? '<p class="notice">新地址已分配，但服务器内的网络配置可能需要更新，业务可用性尚未确认。</p>' : ""}</div>`;
}

export function openIPManagement(instance, acc, deps) {
  const {
    api,
    esc,
    openDialog,
    dialog,
    intentID,
    submit,
    formAction,
    createReplacement,
    viewConnectivity,
    refreshInventory,
  } = deps;
  const identity = {
    accountId: acc.id,
    region: instance.region,
    target: instance.id,
  };
  let alive = true;
  openDialog(
    "公网 IP 管理",
    '<div class="ip-management"><p class="loading">正在查询实际公网 IP 和可用方式…</p></div>',
  );
  const pane = dialog.querySelector(".ip-management");
  dialog.addEventListener(
    "close",
    () => {
      alive = false;
    },
    { once: true },
  );

  async function load() {
    pane.innerHTML = '<p class="loading">正在查询实际公网 IP 和可用方式…</p>';
    try {
      const response = await api("ip?" + new URLSearchParams(identity));
      if (!alive || !pane.isConnected) return;
      const info = response?.data;
      if (
        response.accountRevision !== acc.revision ||
        !info ||
        info.instanceId !== instance.id ||
        info.region !== instance.region ||
        typeof info.snapshot !== "string" ||
        !Array.isArray(info.methods)
      )
        throw Error("云端 IP 信息与当前服务器不一致，请刷新后重试");
      const current = info.primaryIPv4 || "尚未分配或未确认";
      const extras = list(info.addresses).filter(
        (a) => a?.address && !a.primary,
      );
      const service = list(info.serviceIPv4).filter(
        (s) => typeof s === "string" && s,
      );
      const available = list(info.availableIPs).filter((a) => a?.address);
      const methods = list(info.methods).filter(
        (m) => m && typeof m.id === "string",
      );
      const detected = [
        ...new Set(
          [
            info.primaryIPv4,
            ...extras.map((a) => a.address),
            ...service,
          ].filter(Boolean),
        ),
      ];
      pane.innerHTML = `<p class="sub">${esc(instance.name)} · ${esc(acc.name)} · <span class="mono">${esc(instance.id)}</span></p><div class="error-box" role="alert" data-ip-error></div><div class="ip-addresses"><div><span>原始公网 IPv4</span><strong class="mono">${esc(current)}</strong></div><div><span>额外 IPv4 / Reserved IP</span><strong>${extras.length ? extras.map((a) => `<span class="mono">${esc(a.address)}</span> <small>${esc(readableAddress(a))}</small>`).join("<br>") : "无"}</strong></div><div><span>实际对外服务 IP</span><strong class="mono">${service.length ? service.map(esc).join(" / ") : "未确认"}</strong></div></div>${resultNotice(info, esc)}<section class="ip-section"><h3>最近国内检测</h3>${lastCheck(response.lastCheck, esc)}<button type="button" data-ip-check-open>检测 IP</button><div data-ip-check-panel hidden>${checkFields()}</div></section><section class="ip-section"><h3>更换 IP</h3><p class="help">操作方式取决于此服务器的云端状态。选择前请核对停机、费用和旧地址保留情况。</p>${methods.length ? `<form id="ip-change-form"><div class="error-box" role="alert"></div><div class="ip-methods">${methods.map((m) => `<label class="ip-method ${m.available ? "" : "unavailable"}"><input type="radio" name="method" value="${esc(m.id)}" ${m.available ? "" : "disabled"}><span><strong>${esc(m.name || m.id)}</strong><small>${esc(m.description || "")}</small>${m.requiresStop || m.extraCharge ? `<small>${m.requiresStop ? "可能停机或中断业务" : ""}${m.requiresStop && m.extraCharge ? " · " : ""}${m.extraCharge ? "可能增加费用" : ""}</small>` : ""}${!m.available ? `<small class="ip-unavailable">${esc(m.reason || "当前不可执行")}</small>` : ""}</span></label>`).join("")}</div>${available.length ? `<label class="field">使用已有可分配地址（如所选方式需要）<select name="available"><option value="">由云端新分配或按所选方式执行</option>${available.map((a, n) => `<option value="${n}">${esc(a.address)} · ${esc(readableAddress(a))}${a.resourceId ? " · " + esc(a.resourceId) : ""}</option>`).join("")}</select></label>` : ""}<label class="check"><input type="checkbox" name="postCheck">更换后自动检测新 IP</label><p class="help">自动检测使用上方检测表单中的探针和协议设置；需先确认目标服务已监听。</p><p class="notice" data-ip-risk>选择一种可执行方式以查看操作说明。</p><div class="dialog-actions"><button class="primary" type="submit" disabled>确认更换 IP</button></div></form>` : '<p class="help">云端没有提供可用的原机更换方式。</p>'}${acc.provider === "digitalocean" ? '<p class="notice">Reserved IP 是额外对外服务地址，不会替换 Droplet 原始公网 IPv4；普通 Droplet 不支持原机随机更换原始 IP。</p>' : ""}${acc.provider === "linode" ? '<p class="help">Linode 额外 IPv4 受配额、权限和网络接口代际限制。新增地址不等于原始 IP 已更换。</p>' : ""}<button type="button" data-ip-replacement>创建替代实例</button><p class="help">独立新建并产生费用；原服务器不会自动删除。可在创建表单修改配置。</p></section><div class="dialog-actions"><button type="button" data-ip-refresh>刷新实际 IP</button><button type="button" data-close>关闭</button></div>`;
      pane.querySelector("[data-ip-refresh]").onclick = () => {
        refreshInventory();
        load();
      };
      pane.querySelector("[data-ip-replacement]").onclick = () =>
        createReplacement({
          region: instance.region,
          name: `${instance.name.slice(0, 59)}-new`,
        });
      const checkPanel = pane.querySelector("[data-ip-check-panel]");
      const checkForm = pane.querySelector("#ip-check-form");
      const checkOpen = pane.querySelector("[data-ip-check-open]");
      let probesLoaded = false;
      let checkID = "";
      const ensureProbes = async () => {
        if (probesLoaded) return;
        const probes = await api("probes");
        if (!Array.isArray(probes.items)) throw Error("探针列表格式无效");
        if (!checkForm.isConnected) return;
        const option = (p) =>
          new Option(
            `${p.name} · ${p.status === "online" ? "在线" : "离线"}`,
            p.id,
          );
        checkForm.elements.probeId.replaceChildren(
          new Option("选择国内探针", ""),
          ...probes.items
            .filter((p) => p.enabled && p.role === "domestic")
            .map(option),
        );
        checkForm.elements.controlProbeId.replaceChildren(
          new Option("Narbe 管理端", "manager"),
          ...probes.items
            .filter((p) => p.enabled && p.role === "control")
            .map(option),
        );
        checkForm.querySelector("[data-ip-probe-status]").textContent =
          probes.items.some((p) => p.enabled && p.role === "domestic")
            ? "检测结果只代表所选探针的实际出口。"
            : "尚无已启用的国内探针，请先到国内连通性添加。";
        probesLoaded = true;
      };
      checkForm.elements.ip.replaceChildren(
        new Option("选择检测地址", ""),
        ...detected.map((ip) => new Option(ip, ip)),
      );
      if (info.primaryIPv4) checkForm.elements.ip.value = info.primaryIPv4;
      const syncProtocol = () => {
        const p = checkForm.elements.protocol.value;
        checkForm.querySelector("[data-ip-server-name]").hidden = ![
          "tls",
          "http",
          "https",
        ].includes(p);
        checkForm.querySelector("[data-ip-http]").hidden = ![
          "http",
          "https",
        ].includes(p);
        if (p === "http" && checkForm.elements.port.value === "22")
          checkForm.elements.port.value = "80";
        if (
          p === "https" &&
          ["22", "80"].includes(checkForm.elements.port.value)
        )
          checkForm.elements.port.value = "443";
      };
      checkForm.elements.protocol.addEventListener("change", syncProtocol);
      syncProtocol();
      checkOpen.onclick = async () => {
        checkPanel.hidden = !checkPanel.hidden;
        checkOpen.textContent = checkPanel.hidden ? "检测 IP" : "收起检测设置";
        if (!checkPanel.hidden) {
          try {
            await ensureProbes();
          } catch (e) {
            checkForm.querySelector(".error-box").textContent = e.message;
          }
        }
      };
      checkForm.addEventListener("input", () => {
        checkID = "";
        checkForm.querySelector("button[type=submit]").disabled = false;
        checkForm.querySelector("[data-ip-check-submitted]")?.remove();
      });
      checkForm.onsubmit = (event) => {
        event.preventDefault();
        formAction(checkForm, async () => {
          await ensureProbes();
          if (!checkForm.elements.probeId.value) throw Error("请选择国内探针");
          checkID ||= intentID();
          if (!checkID) return;
          const response = await api("ip/check", {
            method: "POST",
            body: {
              id: checkID,
              ...identity,
              ip: checkForm.elements.ip.value,
              config: checkConfig(checkForm),
            },
          });
          if (!response?.id)
            throw Error("检测已提交但任务标识未确认，请保留此表单重试核实");
          pane.querySelector("[data-ip-error]").textContent = "";
          checkForm.querySelector("[data-ip-check-submitted]")?.remove();
          checkForm.insertAdjacentHTML(
            "beforeend",
            '<p class="notice" data-ip-check-submitted>检测已提交，可在国内连通性查看进度。</p>',
          );
          checkForm.querySelector("button[type=submit]").disabled = true;
          const view = document.createElement("button");
          view.type = "button";
          view.textContent = "查看检测进度";
          view.onclick = viewConnectivity;
          checkForm.querySelector(".dialog-actions").append(view);
        });
      };
      const changeForm = pane.querySelector("#ip-change-form");
      if (changeForm) {
        let changeID = "";
        checkForm.addEventListener("input", () => {
          changeID = "";
        });
        const syncMethod = () => {
          const selected = methods.find(
            (m) => m.id === changeForm.elements.method.value && m.available,
          );
          const risk = changeForm.querySelector("[data-ip-risk]");
          risk.textContent = selected
            ? `${selected.name}：${selected.description || "请核实云端结果"}。${selected.requiresStop ? "执行可能中断业务。" : ""}${selected.extraCharge ? "可能额外收费。" : ""}旧 IP 的保留与释放以云端实际回读结果为准。`
            : "选择一种可执行方式以查看操作说明。";
          changeForm.querySelector("button[type=submit]").disabled = !selected;
        };
        changeForm.addEventListener("change", () => {
          changeID = "";
          syncMethod();
        });
        changeForm.elements.postCheck.onchange = async () => {
          if (!changeForm.elements.postCheck.checked) return;
          checkPanel.hidden = false;
          checkOpen.textContent = "收起检测设置";
          try {
            await ensureProbes();
          } catch (e) {
            changeForm.querySelector(".error-box").textContent = e.message;
          }
        };
        changeForm.onsubmit = (event) => {
          event.preventDefault();
          formAction(changeForm, async () => {
            const selected = methods.find(
              (m) => m.id === changeForm.elements.method.value && m.available,
            );
            if (!selected) throw Error("请选择当前可执行的换 IP 方式");
            let screening;
            if (changeForm.elements.postCheck.checked) {
              await ensureProbes();
              if (!checkForm.elements.probeId.value) {
                checkPanel.hidden = false;
                throw Error("先在上方选择国内探针和检测条件");
              }
              screening = checkConfig(checkForm);
            }
            changeID ||= intentID();
            if (!changeID) return;
            const choice = changeForm.elements.available?.value;
            const a =
              choice === undefined || choice === ""
                ? null
                : available[Number(choice)];
            const body = {
              id: changeID,
              ...identity,
              action: "ip-change",
              name: instance.name,
              spec: {},
              ipChange: {
                method: selected.id,
                snapshot: info.snapshot,
                ...(a
                  ? {
                      address: a.address,
                      allocationId: a.resourceId || "",
                      interfaceId: a.interfaceId || "",
                      sourceInstanceId: a.instanceId || "",
                    }
                  : {}),
                riskAccepted: true,
              },
              ...(screening ? { screening } : {}),
              confirmation: "",
            };
            await submit(body);
            dialog.close();
          });
        };
      }
    } catch (e) {
      if (alive && pane.isConnected)
        pane.innerHTML = `<p class="error-box" role="alert">${esc(e.message)}</p><button type="button" data-ip-retry>重试读取 IP</button>`;
      pane.querySelector("[data-ip-retry]")?.addEventListener("click", load);
    }
  }
  load();
}
