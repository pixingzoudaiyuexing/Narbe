export async function networkSettings(el, { api, esc, formAction, accounts }) {
  const section = document.createElement("section");
  section.className = "panel network-settings";
  section.innerHTML =
    '<h2>云 API 网络出口</h2><p class="loading">正在读取…</p>';
  el.append(section);
  try {
    let config = await api("network");
    if (!section.isConnected) return;
    section.innerHTML = `<h2>云 API 网络出口</h2><p class="sub">仅影响 Narbe 后端访问四家云厂商 API，不改变新建 VPS 的系统网络。</p><form id="network-form"><div class="error-box" role="alert"></div><label class="field">网络模式<select name="mode"><option value="direct">服务器直连（默认）</option><option value="proxy">代理服务器</option></select></label><div data-proxy-fields><div class="grid2"><label class="field">代理协议<select name="protocol">${["http", "https", "socks5", "socks5h"].map((p) => `<option value="${p}">${p.toUpperCase()}</option>`).join("")}</select></label><label class="field">端口<input name="port" type="number" min="1" max="65535"></label></div><label class="field">代理主机或 IP<input name="host" maxlength="253" autocomplete="off" placeholder="proxy.example.com（不要输入URL）"></label><label class="field">用户名（可选）<input name="username" maxlength="255" autocomplete="off"></label><label class="field">密码处理<select name="passwordAction"><option value="keep">保留原密码</option><option value="replace">替换密码</option><option value="clear">明确清除密码</option></select></label><label class="field" data-password>新密码<input name="password" type="password" maxlength="255" autocomplete="new-password"></label><p class="help" data-secret-status></p><p class="notice">HTTP代理连接本身不加密，用户名/密码可能在代理链路暴露。公网优先HTTPS，或SOCKS5配合安全隧道。SOCKS5与SOCKS5H按Go语义均由代理解析目标域名。</p><p class="help">默认拒绝环回、Metadata及私网地址；私网代理须由服务器管理员通过精确IP:端口安全配置授权。</p><button type="button" data-proxy-test>检测代理连接（未保存配置）</button></div><p class="notice">保存将作废旧确认并取消未派发任务；已运行任务继续使用原出口，不自动转直连。unknown不会重放。</p><button class="primary" type="submit">保存设置</button></form><div class="network-diagnostics"><h3>出口检测（相互独立）</h3><p class="help">IP检测只证明本次测试请求，不能保证每家云API使用相同公网NAT。浏览器本地检测不传云账号或Narbe会话。</p><div class="row-actions"><button data-local-test>检测浏览器本地出口</button><button data-direct-test>检测服务器直连出口</button></div><pre data-local-result aria-live="polite">浏览器本地：未检测</pre><pre data-direct-result aria-live="polite">服务器直连：未检测</pre><pre data-proxy-result aria-live="polite">代理出口：未检测</pre><label class="field">云账号<select data-cloud-account><option value="">选择云账号</option>${accounts()
      .map(
        (a) =>
          `<option value="${esc(a.id)}">${esc(a.provider + " · " + a.name)}</option>`,
      )
      .join(
        "",
      )}</select></label><button data-cloud-test>只读云 API 连通性测试（已保存出口）</button><pre data-cloud-result aria-live="polite">云API：未检测</pre></div>`;
    const f = section.querySelector("form"),
      fields = f.elements;
    function update(v) {
      config = v;
      for (const k of ["mode", "protocol", "host", "port", "username"])
        fields[k].value = v[k] || (k === "protocol" ? "http" : "");
      fields.password.value = "";
      fields.passwordAction.value = "keep";
      section.querySelector("[data-secret-status]").textContent = v.passwordSet
        ? "已保存加密密码（不回显）"
        : "未保存密码";
      sync();
    }
    function sync() {
      const proxy = fields.mode.value === "proxy";
      section.querySelector("[data-proxy-fields]").hidden = !proxy;
      section.querySelector("[data-password]").hidden =
        fields.passwordAction.value !== "replace";
      fields.password.disabled = fields.passwordAction.value !== "replace";
      for (const k of ["host", "port"]) fields[k].required = proxy;
    }
    fields.mode.onchange = sync;
    fields.passwordAction.onchange = () => {
      fields.password.value = "";
      sync();
    };
    const body = () =>
      fields.mode.value === "direct"
        ? {
            revision: config.revision,
            mode: "direct",
            protocol: "",
            host: "",
            port: 0,
            username: "",
            password: "",
            passwordAction: "clear",
          }
        : {
            revision: config.revision,
            mode: "proxy",
            protocol: fields.protocol.value,
            host: fields.host.value,
            port: Number(fields.port.value),
            username: fields.username.value,
            password:
              fields.passwordAction.value === "replace"
                ? fields.password.value
                : "",
            passwordAction: fields.passwordAction.value,
          };
    f.onsubmit = (e) => {
      e.preventDefault();
      formAction(f, async () => {
        update(await api("network", { method: "PUT", body: body() }));
        section.querySelector("[data-secret-status]").textContent +=
          "；设置已保存";
      });
    };
    const display = (target, label, v) => {
      if (target.isConnected)
        target.textContent =
          label +
          "：" +
          (v.ip || v.message || v.status) +
          `\n${v.checkedAt ? new Date(v.checkedAt * 1000).toLocaleString() : ""} · ${v.latencyMs ?? 0} ms · ${v.status}`;
    };
    async function test(kind, configDraft) {
      const target = section.querySelector(
        `[data-${kind === "cloud" ? "cloud" : kind}-result]`,
      );
      target.textContent = "检测中…";
      const admitted = Date.now();
      const buttons = [
        ...section.querySelectorAll(
          "[data-proxy-test],[data-direct-test],[data-cloud-test]",
        ),
      ];
      buttons.forEach((b) => (b.disabled = true));
      try {
        const v = await api("network/test", {
          method: "POST",
          body: {
            kind,
            ...(configDraft ? { config: configDraft } : {}),
            ...(kind === "cloud"
              ? {
                  accountId: section.querySelector("[data-cloud-account]")
                    .value,
                }
              : {}),
          },
        });
        display(
          target,
          { direct: "服务器直连", proxy: "代理出口", cloud: "云API" }[kind],
          v,
        );
      } catch (e) {
        if (target.isConnected) target.textContent = e.message;
      } finally {
        setTimeout(
          () => {
            buttons.forEach((b) => {
              if (b.isConnected) b.disabled = false;
            });
          },
          Math.max(0, 1100 - (Date.now() - admitted)),
        );
      }
    }
    section.querySelector("[data-proxy-test]").onclick = () =>
      test("proxy", body());
    section.querySelector("[data-direct-test]").onclick = () => test("direct");
    section.querySelector("[data-cloud-test]").onclick = () => test("cloud");
    section.querySelector("[data-local-test]").onclick = async () => {
      const out = section.querySelector("[data-local-result]");
      out.textContent = "浏览器本地检测中…";
      try {
        const start = Date.now();
        const res = await fetch("https://api64.ipify.org?format=json", {
          credentials: "omit",
          referrerPolicy: "no-referrer",
          cache: "no-store",
          redirect: "error",
          signal: AbortSignal.timeout(10000),
        });
        if (!res.ok) throw Error("IP检测服务失败");
        const raw = await res.text();
        if (raw.length > 1024) throw Error("IP响应过长");
        const v = JSON.parse(raw);
        if (typeof v.ip !== "string" || !/^[0-9a-fA-F:.]{3,45}$/.test(v.ip))
          throw Error("IP响应无效");
        display(out, "浏览器本地（非云API出口）", {
          ...v,
          status: "success",
          checkedAt: Date.now() / 1000,
          latencyMs: Date.now() - start,
        });
      } catch {
        if (out.isConnected)
          out.textContent =
            "浏览器本地出口检测失败（浏览器网络/CORS/服务不可达），不代表云API失败";
      }
    };
    update(config);
  } catch (e) {
    if (section.isConnected)
      section.innerHTML =
        '<h2>云 API 网络出口</h2><p class="error-box">' +
        esc(e.message) +
        "</p>";
  }
}
