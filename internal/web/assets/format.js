export function memoryLabel(mib) {
  return mib > 0 ? `${Number((mib / 1024).toFixed(2))} GB 内存` : "内存未确认";
}
export function configLabel(value, provider) {
  const cpu = value.cpu > 0 ? `${value.cpu} vCPU` : "CPU 未确认";
  const arch = value.architecture || "架构未确认";
  let disk;
  if (provider === "aws")
    disk =
      value.disk > 0
        ? `${value.disk} GB 实例存储；EBS 系统盘另计`
        : "EBS 系统盘独立计费，容量另选";
  else if (provider === "azure")
    disk =
      value.disk > 0
        ? `${value.disk} GB Managed Disk 系统盘（独立计费）`
        : "Managed Disk 系统盘独立配置、计费";
  else
    disk =
      value.disk > 0
        ? `${value.disk} GB ${value.diskKind || "套餐磁盘"}`
        : "磁盘容量未确认";
  return `${cpu} / ${memoryLabel(value.memory)} / ${disk} / ${arch}`;
}
export function priceLabel(pricing) {
  if (
    !pricing ||
    pricing.currency !== "USD" ||
    !(pricing.monthly > 0 || pricing.hourly > 0)
  )
    return "当前无法获取准确报价，请以云厂商账单为准。";
  return [
    pricing.monthly > 0 ? `${pricing.monthly} USD/月` : "",
    pricing.hourly > 0 ? `${pricing.hourly} USD/小时` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}
export function stateGroup(state) {
  if (["running", "active", "on"].includes(state)) return "running";
  if (["stopped", "off", "offline", "deallocated"].includes(state))
    return "stopped";
  return "other";
}
export function serverStateLabel(state) {
  const labels = {
    running: "运行中",
    active: "运行中",
    on: "运行中",
    offline: "已关机",
    off: "已关机",
    stopped: "已关机",
    deallocated: "已关机并释放计算分配",
    pending: "准备启动",
    new: "创建中",
    creating: "创建中",
    provisioning: "创建中",
    booting: "开机中",
    starting: "开机中",
    stopping: "关机中",
    shutting_down: "关机中",
    deallocating: "正在释放计算分配",
    rebooting: "重启中",
    "shutting-down": "正在删除",
    terminated: "已删除",
    deleting: "正在删除",
    archive: "已归档",
    busy: "正在处理操作",
    migrating: "迁移中",
    rebuilding: "重装系统中",
    cloning: "克隆中",
    restoring: "恢复中",
    resizing: "调整配置中",
    updating: "更新中",
    billing_suspension: "欠费暂停",
    succeeded: "资源就绪，电源状态未确认",
    failed: "资源状态异常",
    unknown: "状态未确认",
  };
  return labels[state] || (state ? `其他状态（${state}）` : "状态未确认");
}
export function regionLabel(regions, id, fallback = "") {
  const name = regions?.find((r) => r.id === id)?.name;
  if (name) return name;
  return fallback || `${id || "地区未知"}（位置名称未确认）`;
}
export function inventoryMessage(snapshot) {
  if (!snapshot || ["loading", "queued"].includes(snapshot.phase))
    return "正在查询资源，服务器总数尚未确认…";
  if (!snapshot.complete)
    return `已发现 ${snapshot.stats.total} 台服务器，另有 ${snapshot.failures.length} 个地区或查询范围失败，总数暂未确认。`;
  return `共 ${snapshot.stats.total} 台服务器`;
}
