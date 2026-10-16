import {
  startupLimits,
  startupLabels,
  startupPayload,
  utf8Bytes,
  startupHash,
  startupCompatibility,
  stackParameterJSON,
} from "./startup-format.js";
export function startupFields() {
  return `<section class="startup-fields"><h3>初始化脚本（可选）</h3><label class="field">脚本模式<select name="scriptMode"><option value="none">不使用初始化脚本</option></select></label><p class="help" data-startup-compat></p><div data-startup-text hidden><label class="field">脚本原始内容<textarea name="userData" class="code-editor" rows="9" spellcheck="false" autocomplete="off" autocapitalize="off" aria-describedby="startup-bytes" placeholder="粘贴原始多行脚本，不要Base64编码"></textarea></label><p class="help" id="startup-bytes" data-startup-bytes></p><details><summary>简单语法示例</summary><pre data-startup-example></pre><button type="button" data-use-example>填入示例（仅空编辑框）</button></details><label class="check" data-startup-ack hidden><input name="scriptCompatibilityAck" type="checkbox">我已核实该镜像的初始化代理兼容性，理解无法保证脚本执行结果</label></div><div data-stackscript hidden><div class="grid2"><label class="field">脚本来源<select name="stackScope"><option value="mine">我的脚本</option><option value="public">公共脚本</option><option value="accessible">全部可访问</option></select></label><label class="field">搜索名称<input name="stackSearch" type="search" maxlength="128"></label></div><button type="button" data-stack-search>查询StackScripts</button><label class="field">已有StackScript<select name="stackID"><option value="">查询后选择</option></select></label><p data-stack-description></p><p class="help" data-stack-status></p><div class="row-actions"><button type="button" data-stack-prev disabled>上一页</button><span data-stack-page></span><button type="button" data-stack-next disabled>下一页</button></div><div data-stack-udf></div><p class="notice">公共脚本是第三方root代码，请只使用可信来源。参数可能包含敏感信息，不会显示在任务详情。</p></div><p class="error-box" role="alert" data-startup-error></p><p data-startup-warning hidden class="help">首次启动由root或系统初始化代理处理，可能延长开机时间。云厂商及虚拟机内可能保留User Data；Base64不是加密。不要在脚本中硬编码API Token、私钥或云密码。Narbe只提交脚本，不在容器内运行，不通过SSH远程执行。</p></section>`;
}
const examples = {
  shell: '#!/bin/bash\nprintf "%s\\n" "初始化开始"\n',
  "cloud-config": "#cloud-config\npackage_update: true\npackages:\n  - nginx\n",
  powershell: 'Write-Output "Initialization started"\n',
};
export function bindStartup({
  form,
  provider,
  aid,
  revision,
  api,
  esc,
  options,
  changed,
}) {
  const root = form.querySelector(".startup-fields"),
    mode = form.elements.scriptMode,
    text = form.elements.userData;
  let catalog,
    image,
    region,
    seq = 0,
    controller,
    chosen = null,
    page = 1,
    totalPages = 1,
    list = [],
    busy = false,
    loadError = "",
    lastCompatibility = "";
  const drafts = new Map(),
    udfDrafts = new Map();
  let previous = "none";
  const enabledModes = () => {
    const windows = image?.family === "windows";
    return [
      "none",
      ...(provider === "aws" && windows
        ? ["powershell"]
        : provider === "azure" && windows
          ? []
          : ["shell", "cloud-config"]),
      ...(provider === "linode" && !windows ? ["stackscript"] : []),
    ];
  };
  function fieldValues() {
    if (!chosen) return Object.create(null);
    const values = Object.create(null);
    root.querySelectorAll("[data-udf]").forEach((el) => {
      const field = chosen.user_defined_fields[Number(el.dataset.udf)];
      values[field.name] = el.multiple
        ? Array.from(el.selectedOptions, (o) => o.value).join(",")
        : el.value;
    });
    return values;
  }
  function saveUDF() {
    if (chosen) udfDrafts.set(chosen.id, fieldValues());
  }
  function valid() {
    const support = startupCompatibility(provider, mode.value, image, region);
    if (mode.value === "none") return true;
    if (!support.allowed) return false;
    if (mode.value === "stackscript")
      return (
        String(chosen?.id) === form.elements.stackID.value &&
        !busy &&
        !loadError &&
        !!chosen &&
        (chosen.images.includes(image.id) || chosen.images.includes("any/all"))
      );
    return (
      text.value.trim() !== "" &&
      utf8Bytes(startupPayload(mode.value, text.value)) <=
        startupLimits[provider] &&
      (!support.ack || form.elements.scriptCompatibilityAck.checked)
    );
  }
  function sync() {
    root
      .querySelectorAll("[data-udf]")
      .forEach((el) => (el.disabled = mode.value !== "stackscript"));
    const support = startupCompatibility(provider, mode.value, image, region),
      active = mode.value !== "none",
      stack = mode.value === "stackscript";
    root.querySelector("[data-startup-text]").hidden = !active || stack;
    root.querySelector("[data-stackscript]").hidden = !stack;
    root.querySelector("[data-startup-warning]").hidden = !active;
    root.querySelector("[data-startup-compat]").textContent = support.note;
    root.querySelector("[data-startup-ack]").hidden = !support.ack;
    const payloadBytes = utf8Bytes(startupPayload(mode.value, text.value));
    root.querySelector("[data-startup-bytes]").textContent =
      `原始UTF-8 ${utf8Bytes(text.value)} 字节 · 提交内容 ${payloadBytes} / ${startupLimits[provider]} 字节${provider === "linode" ? "（Base64后最多65535字节）" : ""}`;
    root.querySelector("[data-startup-example]").textContent =
      examples[mode.value] || "";
    let error = "";
    if (active && !support.allowed) error = support.note;
    else if (active && !stack && payloadBytes > startupLimits[provider])
      error = "脚本UTF-8字节超限，请缩短内容";
    else if (
      stack &&
      chosen &&
      image &&
      !chosen.images.includes(image.id) &&
      !chosen.images.includes("any/all")
    )
      error = "StackScript与当前系统镜像不兼容，请重新选择";
    root.querySelector("[data-startup-error]").textContent = loadError || error;
  }
  function update(next) {
    catalog = next;
    image = catalog?.images?.find(
      (i) => i.id === (form.elements.image || form.elements.newImage).value,
    );
    region = catalog?.regions?.find(
      (r) =>
        r.id ===
        (form.elements.region?.value || form.dataset.startupRegion || ""),
    );
    const value = mode.value,
      all = enabledModes();
    if (value !== "none" && !all.includes(value)) all.push(value);
    mode.innerHTML = all
      .map(
        (k) =>
          `<option value="${k}" ${k === value ? "selected" : ""} ${!enabledModes().includes(k) ? "disabled" : ""}>${startupLabels[k]}${!enabledModes().includes(k) ? "（与当前镜像不兼容，内容保留）" : ""}</option>`,
      )
      .join("");
    const identity = [
      form.elements.region?.value || form.dataset.startupRegion || "",
      (form.elements.image || form.elements.newImage).value,
    ].join("|");
    if (identity !== lastCompatibility) {
      form.elements.scriptCompatibilityAck.checked = false;
      lastCompatibility = identity;
    }
    sync();
  }
  function spec() {
    if (!valid()) throw Error("初始化脚本尚未完成或与镜像不兼容，请检查表单");
    if (mode.value === "none") return { scriptMode: "none", userData: "" };
    if (mode.value === "stackscript")
      return {
        scriptMode: "stackscript",
        userData: "",
        stackscriptId: chosen.id,
        stackscriptName: chosen.label,
        stackscriptSnapshot: chosen.snapshot,
        stackscriptData: fieldValues(),
      };
    return {
      scriptMode: mode.value,
      userData: text.value,
      scriptCompatibilityAck: !!form.elements.scriptCompatibilityAck.checked,
    };
  }
  function summary() {
    if (mode.value === "none") return "不使用初始化脚本";
    const stack = mode.value === "stackscript",
      raw = stack ? stackParameterJSON(fieldValues()) : text.value;
    return `${startupLabels[mode.value]}${stack ? ` · ${chosen?.label || "尚未选择"} · ID ${chosen?.id || "—"}` : ""} · ${stack ? "参数 " : ""}${utf8Bytes(raw)} UTF-8字节 · ${stack ? "参数 " : ""}SHA-256 ${startupHash(raw)}${stack ? ` · 脚本及定义版本 SHA-256 ${chosen?.snapshot || "未确认"}` : ""} · 执行结果未验证`;
  }
  async function query(detailID) {
    const current = ++seq;
    controller?.abort();
    controller = new AbortController();
    busy = true;
    loadError = "";
    if (!detailID) saveUDF();
    if (detailID) chosen = null;
    else {
      chosen = null;
      root.querySelector("[data-stack-udf]").innerHTML = "";
      root.querySelector("[data-stack-description]").textContent = "";
    }
    sync();
    changed();
    try {
      const query = new URLSearchParams({
        accountId: aid,
        ...(detailID
          ? { id: String(detailID) }
          : {
              scope: form.elements.stackScope.value,
              search: form.elements.stackSearch.value,
              page: String(page),
            }),
      });
      const reply = await api("stackscripts?" + query, {
        signal: controller.signal,
      });
      if (
        current !== seq ||
        !form.isConnected ||
        mode.value !== "stackscript" ||
        (detailID && String(detailID) !== form.elements.stackID.value)
      )
        return;
      if (reply.accountId !== aid || reply.accountRevision !== revision)
        throw Error("账号凭据已变化，请重开表单");
      const data = reply.data;
      if (detailID) {
        chosen = data;
        const saved = udfDrafts.get(data.id) || Object.create(null);
        root.querySelector("[data-stack-description]").textContent =
          data.description;
        root.querySelector("[data-stack-udf]").innerHTML =
          data.user_defined_fields
            .map((field, index) => {
              const name = `udf-${index}`,
                required =
                  field.default === null || field.default === undefined,
                value =
                  (Object.hasOwn(saved, field.name)
                    ? saved[field.name]
                    : field.default) ?? "",
                choices = field.oneOf || field.manyOf;
              return `<label class="field">${esc(field.label)}${required ? "（必填）" : "（可选）"}${
                choices
                  ? `<select data-udf="${index}" name="${name}" ${field.manyOf ? "multiple" : ""} ${required ? "required" : ""}>${field.manyOf ? "" : '<option value="">请选择</option>'}${choices
                      .split(",")
                      .map(
                        (v) =>
                          `<option value="${esc(v)}" ${(field.manyOf ? value.split(",").includes(v) : value === v) ? "selected" : ""}>${esc(v)}</option>`,
                      )
                      .join("")}</select>`
                  : `<input data-udf="${index}" name="${name}" type="${/password|secret|token/i.test(field.name + field.label) ? "password" : "text"}" value="${esc(value)}" ${required ? "required" : ""} autocomplete="off" maxlength="32768">`
              }<span class="help">${esc(field.example || "")}</span></label>`;
            })
            .join("");
        root.querySelector("[data-stack-status]").textContent =
          `${data.mine ? "我的脚本" : data.is_public ? "公共脚本" : "账号可访问脚本"} · ${data.username} · 版本指纹 ${data.snapshot}`;
      } else {
        list = data.items;
        totalPages = data.pages;
        form.elements.stackID.innerHTML = options(
          list.map((s) => ({
            id: String(s.id),
            name: `${s.label} · ${s.mine ? "我的" : s.is_public ? "公共" : "可访问"} · ${s.id}`,
          })),
          chosen ? String(chosen.id) : "",
        );
        root.querySelector("[data-stack-page]").textContent =
          `第${data.page}/${data.pages}页，共${data.results}个`;
        root.querySelector("[data-stack-prev]").disabled = page <= 1;
        root.querySelector("[data-stack-next]").disabled = page >= totalPages;
      }
    } catch (e) {
      if (current === seq && !controller.signal.aborted)
        loadError = e.message + "；请检查stackscripts:read_only权限并重试";
    } finally {
      if (current === seq && form.isConnected) {
        busy = false;
        sync();
        changed();
      }
    }
  }
  mode.onchange = () => {
    drafts.set(previous, text.value);
    saveUDF();
    previous = mode.value;
    text.value = drafts.get(mode.value) || "";
    loadError = "";
    if (mode.value !== "stackscript") {
      ++seq;
      controller?.abort();
      busy = false;
    }
    sync();
    changed();
    if (mode.value === "stackscript" && !list.length) query();
  };
  text.oninput = () => {
    drafts.set(mode.value, text.value);
    sync();
    changed();
  };
  root.querySelector("[data-use-example]").onclick = () => {
    if (!text.value) {
      text.value = examples[mode.value] || "";
      sync();
      changed();
    }
  };
  root.querySelector("[data-stack-search]").onclick = () => {
    page = 1;
    query();
  };
  form.elements.stackScope.onchange = () => {
    page = 1;
    query();
  };
  form.elements.stackID.onchange = () => {
    saveUDF();
    root.querySelector("[data-stack-udf]").innerHTML = "";
    if (form.elements.stackID.value) query(Number(form.elements.stackID.value));
    else {
      ++seq;
      controller?.abort();
      busy = false;
      loadError = "";
      chosen = null;
      sync();
      changed();
    }
  };
  for (const [selector, delta] of [
    ["[data-stack-prev]", -1],
    ["[data-stack-next]", 1],
  ])
    root.querySelector(selector).onclick = () => {
      page += delta;
      query();
    };
  root.addEventListener("input", () => {
    sync();
    changed();
  });
  root.addEventListener("change", () => {
    sync();
    changed();
  });
  form.closest("dialog").addEventListener(
    "close",
    () => {
      ++seq;
      controller?.abort();
      drafts.clear();
      udfDrafts.clear();
      text.value = "";
      chosen = null;
      root.querySelector("[data-stack-udf]").innerHTML = "";
    },
    { once: true },
  );
  update(null);
  return { update, spec, summary, valid };
}
