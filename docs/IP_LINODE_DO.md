# Linode 与 DigitalOcean 原机 IP 管理

核对日期：2026-11-18。下列结论基于当前官方 API 文档和本地 HTTP 合同测试；没有使用真实云凭据，也没有进行真实云写入或来宾系统连通性验证。

## Linode

- `legacy_config` 增加公网 IPv4：`POST /v4/linode/instances/{id}/ips`，请求 `{"type":"ipv4","public":true}`。这是**额外** IPv4，不替换主地址。新地址必须向支持请求额外配额，额外地址有月费。[官方 API](https://techdocs.akamai.com/linode-api/reference/post-add-linode-ip)
- `linode` 网络接口代际：`POST /v4/networking/ips` 明确不适用于 Linode Interfaces；已有 public interface 的增址使用 `PUT /v4/linode/instances/{id}/interfaces/{interfaceId}`，`public.ipv4.addresses` 必须列出全部原地址并追加 `{"address":"auto","primary":false}`。遗漏的原地址会被移除。运行中实例先关机，回读确认后更新接口，原本运行的实例再开机。[接口更新 API](https://techdocs.akamai.com/linode-api/reference/put-linode-interface) · [2026-04-09 变更说明](https://techdocs.akamai.com/linode-api/changelog/apr-9-2026-linode-interfaces-and-firewall-templates)
- 账号内同地区的保留 IPv4 可用 `POST /v4/networking/ips/assign` 分配给目标；来自其他 Linode 的候选必须明确记录来源，并核实来源仍有另一公网 IPv4。转移会使来源失去此地址。[分配 API](https://techdocs.akamai.com/linode-api/reference/post-assign-ips)
- 公网地址、代际、主次标记和归属分别通过实例、实例 IP、接口及账号 IP 列表回读。只读 API 不提供可靠的额外 IPv4 剩余额度或最终资源写授权证明，因此界面提示未知，真正的配额拒绝仅在云端明确返回 IPv4 limit/quota 类 400 时归类为 `ErrIPQuota`。权限拒绝、通用 400、超时与响应丢失均不得伪装为配额不足。[IP 列表 API](https://techdocs.akamai.com/linode-api/reference/get-ips) · [IPv4 配额说明](https://techdocs.akamai.com/cloud-computing/docs/managing-ip-addresses-on-a-compute-instance)

## DigitalOcean

- 普通 Droplet 公网 IPv4 不具备保留原 Droplet 原地随机更换的 API；Rebuild 不保证换 IP。已有 Droplet 的原始公网地址始终单独显示。
- `POST /v2/reserved_ips` 携带 `{"droplet_id":123}` 可创建并绑定新 Reserved IP。已有同地区 Reserved IP 使用 `POST /v2/reserved_ips/{ip}/actions`，携带 `{"type":"assign","droplet_id":123}`；Action 完成后仍须回读地址、地区与 Droplet 绑定。[Reserved IP API](https://docs.digitalocean.com/reference/api/reference/reserved-ips/) · [Action API](https://docs.digitalocean.com/reference/api/reference/reserved-ip-actions/)
- Reserved IP 是额外服务地址，原始 Droplet IPv4 不变。每台 Droplet 至多绑定一个 Reserved IP；已有绑定时不自动移走或删除旧地址。迁移其他 Droplet 的 Reserved IP 会令来源失去该服务地址。Reserved IP 的配额、收费和 anchor IP 要求以官方规则及云端返回为准。[限制](https://docs.digitalocean.com/products/networking/reserved-ips/details/limits/) · [功能](https://docs.digitalocean.com/products/networking/reserved-ips/details/features/)
- 候选读取完整分页的 Reserved IP 列表。绑定字段缺失、身份或地区不明时拒绝选择；无关 Reserved IP 不构成账号级锁。创建响应丢失导致新 IP 身份未知时只保留 `dispatching` 账本并停止，绝不重新发送创建请求。即使云端已绑定，也须由后续人工核实与来宾服务检测确认可用。

## 操作边界

两家适配器均要求实例 ID、地区、配置快照和明确风险确认。`AdvanceIPChange` 一次最多派发一笔云写，每笔写之前持久化操作账本；`ReconcileIPChange` 只调用云只读 API。已成功分配的附加地址保留，旧 IP 默认不释放。云 API 回读只能证明资源绑定，不能证明来宾系统已配置网络、目标端口可达、DNS 已切换或中国大陆探针检测通过；这些属于后续工作流独立证据。
