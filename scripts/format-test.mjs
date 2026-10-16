import assert from "node:assert/strict";
import {
  configLabel,
  priceLabel,
  stateGroup,
  regionLabel,
  inventoryMessage,
  serverStateLabel,
} from "../internal/web/assets/format.js";
assert.equal(
  configLabel(
    { cpu: 2, memory: 4096, disk: 80, architecture: "x86_64" },
    "linode",
  ),
  "2 vCPU / 4 GB 内存 / 80 GB 套餐磁盘 / x86_64",
);
assert.ok(
  configLabel({ cpu: 2, memory: 4096 }, "aws").includes("EBS 系统盘独立计费"),
);
assert.ok(
  configLabel({ disk: 100 }, "aws").includes("100 GB 实例存储；EBS 系统盘另计"),
);
assert.ok(
  configLabel({ disk: 64 }, "azure").includes(
    "64 GB Managed Disk 系统盘（独立计费）",
  ),
);
assert.ok(
  configLabel({}, "digitalocean").includes(
    "CPU 未确认 / 内存未确认 / 磁盘容量未确认 / 架构未确认",
  ),
);
assert.equal(
  priceLabel({ monthly: 12, hourly: 0.02, currency: "USD" }),
  "12 USD/月 · 0.02 USD/小时",
);
assert.equal(priceLabel({ hourly: 0.02, currency: "USD" }), "0.02 USD/小时");
for (const p of [
  undefined,
  {},
  { monthly: 0, hourly: 0, currency: "USD" },
  { monthly: 10 },
])
  assert.ok(priceLabel(p).includes("无法获取准确报价"));
assert.equal(
  regionLabel([{ id: "r", name: "日本东京 · r" }], "r"),
  "日本东京 · r",
);
assert.ok(regionLabel([], "new-id").includes("位置名称未确认"));
assert.equal(stateGroup("deallocated"), "stopped");
assert.equal(stateGroup("new-provider-state"), "other");
assert.ok(inventoryMessage({ phase: "queued" }).includes("尚未确认"));
assert.equal(
  inventoryMessage({
    phase: "ready",
    complete: false,
    stats: { total: 8 },
    failures: [{}, {}],
  }),
  "已发现 8 台服务器，另有 2 个地区或查询范围失败，总数暂未确认。",
);
console.log(
  "PASS: four-cloud CPU/memory/disk/currency/hourly/unknown price, regions and incomplete count display.",
);

assert.equal(serverStateLabel("billing_suspension"), "欠费暂停");
assert.equal(serverStateLabel("succeeded"), "资源就绪，电源状态未确认");
assert.equal(serverStateLabel("future-state"), "其他状态（future-state）");

assert.equal(serverStateLabel("stopped", "azure"), "已关机（计算仍计费）");
assert.equal(serverStateLabel("stopped", "aws"), "已关机");

const startup = await import("../internal/web/assets/startup-format.js");
const { createHash } = await import("node:crypto");
for (const text of ["", "abc", '中文💻\n"quoted" <> &', "x".repeat(65536)])
  assert.equal(
    startup.startupHash(text),
    createHash("sha256").update(text).digest("hex"),
  );
assert.equal(
  startup.stackParameterJSON({ value: "usual2028中文\u2028", A: "<>&" }),
  '{"A":"\\u003c\\u003e\\u0026","value":"usual2028中文\\u2028"}',
);
assert.equal(startup.utf8Bytes("中文💻"), 10);
assert.equal(startup.startupLimits.linode, 49149);
console.log(
  "PASS: startup SHA256 vectors, canonical StackScript parameters, UTF-8 bytes and limits",
);
