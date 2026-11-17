# CN Connectivity 开发候选复审入口

基线：`v0.1.0-beta.5` / `80d326fa5ddbd777709a023825d171d3fd69e0ff`。分支 `codex/cn-connectivity`。精确候选 SHA、Push/PR CI 链接和下载产物以候选 PR / Engineer 交付报告为准。

本任务新增自动收费与删除，属于 High 风险。此文档没有声明正式独立审核 PASS。此前 Owner 对 beta.5 发行免额外人工复审的授权只适用于当次 beta.5，不延伸为本次功能的发行授权。完成后交回 Project + Product Manager 决定后续审核与发行；当前不合并 main、不创建新版本 Tag/Release、不修改 Owner 部署。

## 必审边界

- `internal/app/connectivity*.go`：不可篡改创建参数、加密凭据/出口快照、费用预留、工作流领取、停止/重启/unknown、证据与指纹、专用只读核实。
- `internal/cloud/screening*.go` 与 `dispatch.go`：本轮资源所有权、每笔写前检查、AWS stop/start、Azure 新旧 PIP/完整 NIC、Linode/DO 删除确认、未知写不重放。
- `internal/probe/`、`cmd/narbe-probe/`：公网目标与本地端口白名单、固定 IP/禁重定向/TLS、出口证据、持久身份与租约、防重放、结果幂等。
- `internal/store/connectivity.go` 与 Open 恢复：新增表、密钥核实后迁移、同 Volume 持久化、无破坏性迁移。
- `internal/web/assets/connectivity-ui.js` 及 hooks：默认关闭、一次风险确认、密码不暴露、逐轮资源与停止状态、桌面/移动页面。
- `Dockerfile*`、Compose、`ci.yml`：非 root/只读、Go 安全补丁、双原生架构、实际容器健康和升级、最小权限、候选镜像可追溯。

## 已有工程证据与反例

- 工作流：国内/海外成功、连接级失败、同时失败/无监听、慢启动、离线/出口错误、重复地址、次数/费用上限、停止竞态、凭据保护、同账号多工作流和普通任务并行、unknown 不重放、全部 active 状态重启。
- 实际 HTTPS Probe API：一次性/并发注册、认证与 HTTPS Origin、CN/非 CN/未知国家、出口独立核验错误/新鲜度、目标/身份/时间/租约、并发单领取、重复 ACK 幂等、停用与过期、公开查询不返回凭据。
- 云 HTTP 合同：四种替换策略、身份/配置改变、EIP/Reserved IP/复杂拓扑拒绝、Azure 旧 PIP 仍绑定/新 PIP 不确定、各笔写响应丢失与只读确认。
- 红→绿：stop 旧快照覆盖云写账本；无效目标/证书/挑战误计国内失败；换 IP 前费用未预留；独立出口核验错误/过期/未来；负 HTTP 状态；并发相同结果错误 409；账号退役/换版后专用只读核实仍使用旧凭据；DO 主 IPv4 查询 404 漏掉另一个绑定 Reserved IP（含第二页与权限/字段异常）；旧创建窗口空地区竞态；关闭检测额外请求探针 API。
- 云侧六项安全变异已实际变红后恢复通过：fingerprint、mandatory recorder、AWS ownership/EIP、Azure 解绑后删除与 NIC 重读。后端反例的最初失败日志保留在 `.local/cn-connectivity`，没有用编译失败代替行为失败。
- 浏览器实际 App 登录、Go HTTP 替身及原有 beta.3/4/5 功能回归；截图见 `docs/images/cn-connectivity/`，截图中的云资源为替身。
- Docker 原生 runtime、最终完整 race/vet/govulncheck、beta.5→候选升级的实际结果以对应 CI Run 为准，不继承旧版本 PASS。

## 内部检查与保留风险

工程师和有界只读协作者检查不替代正式复审。agy Design Challenger 的“Country.is 明文 HTTP”说法与实际 HTTPS/TLS 代码不符，未采纳；其账号级/全局 unknown 锁建议违反 Owner 明确要求，未实施。采用了新鲜对照证据、写前账本及停止检查，未声称 RTT 或 GeoIP 能证明物理位置。

Azure 官方 NIC PUT 未明确保证 If-Match 原子 CAS；发送 ETag 并重读/后验比较属于防护，真实跨控制台竞态仍需验证。费用是保守预留估算，绝不承诺实际账单上限。所有云操作真实 E2E 和国内目标网络联调均未执行，探针国家与 OS 分流具有已记录限制。

## agy 实际调用记录

4 次调用尝试：Design Challenger 2 次（首次参数缺少 duration 单位而未进入模型，修正后完成 1 次）；Embedded Reviewer 2 次（40 KB 输入超时后缩为 7.5 KB 单一停止/预算问题，仍超时）。两个 Embedded 调用均没有最终 verdict，完成 0 / 失败 2，不能计为审核通过。没有换模型、提高权限或用 Codex 内部协作者结果冒充正式独立审核。开发与自动化验证继续在已授权范围内完成。

末轮 DO 补充任务的内部协作者在完成失败合同测试后遭遇 `model_not_found`，未完成实现；由主工程师直接完成最小修复、验证其红→绿，未把失败调用计为审核通过。
