# beta.3 官方API依据与保守边界

核实日期：2026-10-15。Linode当前官方API参考及 linode-api-openapi 4.229.1；旧linode-api-docs归档版本不作为当前契约。部分官方schema/示例有差异，下文明确处理方式。只读研究不是实际云写E2E。

| 能力 | 官方API与依据 | 实现判断 |
|---|---|---|
| 重装 | [Rebuild](https://techdocs.akamai.com/linode-api/reference/post-rebuild-linode-instance)、[SSH-only变更](https://techdocs.akamai.com/linode-api/changelog/jun-3-2026-password-less-linode-support) | POST image+root_pass或authorized_keys，booted=true；不删实例重建，不顺带改type；disk/config被删除、alerts需复查。schema旧required root_pass与新说明不一致，按新SSH-only说明。 |
| 调配置 | [Resize API](https://techdocs.akamai.com/linode-api/reference/post-resize-linode-instance)、[指南](https://techdocs.akamai.com/cloud-computing/docs/resize-a-compute-instance) | type目标套餐，migration_type=cold，allow_auto_disk_resize=false；实际本地disk MB总量不能超过目标。电源恢复描述存在差异，只以操作后GET状态为准，不追加自动开关机。 |
| root密码 | [指定disk密码](https://techdocs.akamai.com/linode-api/reference/post-reset-disk-password) | password字段，不是实例级root_pass；先offline，明确ext3/ext4磁盘ID，其他特殊结构拒绝。密码只进入加密intent，不返回任务详情。 |
| 克隆 | [Clone API](https://techdocs.akamai.com/linode-api/reference/post-clone-linode-instance)、[官方集成测试](https://github.com/linode/linodego/blob/main/test/integration/instances_test.go) | region/type/label，无linode_id、无不支持的tags字段；全部configs及引用disk+额外未引用disk。为数据一致性Narbe要求源offline（保守政策）。新机ID与源ID分开，等源linode_clone且secondary目标ID匹配后才规范化新机防火墙。 |
| 跨DC迁移 | [Migrate](https://techdocs.akamai.com/linode-api/reference/post-migrate-linode-instance)、[数据中心指南](https://techdocs.akamai.com/cloud-computing/docs/migrate-to-a-new-data-center) | region + type=cold + upgrade=false，type不是套餐；IP可变，Backups不迁移，不改DNS。不自动脱离Volumes。 |
| 事件 | [官方轮询指南](https://github.com/linode/linodego/blob/main/docs/event_and_status_polling.md)、[WaitForResourceFree](https://github.com/linode/linodego/blob/main/waitfor.go)、[Events](https://techdocs.akamai.com/linode-api/reference/get-events) | 写前entity.id/type全动作有界分页检查pending，并保存ID水位；随后action+source+disk/clone secondary精确匹配新事件。仅finished作为成功，百分比100/HTTP200不证明完成。secondary可为镜像字符串，不能强解int。无官方幂等header保证，unknown禁止重POST。 |
| 流量 | [当前实例](https://techdocs.akamai.com/linode-api/reference/get-linode-transfer)、[历史](https://techdocs.akamai.com/linode-api/reference/get-linode-transfer-by-year-month)、[账户池](https://techdocs.akamai.com/linode-api/reference/get-transfer) | 实例used为bytes；quota/billable为官方GB，账户池全为GB，显示GiB（2^30 bytes）。兼容bytes_in/out/total示例，缺字段未知。实例quota是贡献额，不是独享剩余。 |
| 监控 | [Stats](https://techdocs.akamai.com/linode-api/reference/get-linode-stats)、[月历史](https://techdocs.akamai.com/linode-api/reference/get-linode-stats-by-year-month)、[官方类型](https://github.com/linode/manager/blob/develop/packages/api-v4/src/linodes/types.ts) | 当前24h；当前年月接口最近30天，过去年月为该月。epoch毫秒、CPU %可超过100、disk Block/s、IPv4/6 bits/s；实际间隔推采样。空/缺失无数据，null点拒绝而不是0。SVG只抽样显示，不从监控推账单流量。 |
| 账单 | [Account](https://techdocs.akamai.com/linode-api/reference/get-account)、[Invoices](https://techdocs.akamai.com/linode-api/reference/get-invoices)、[Items](https://techdocs.akamai.com/linode-api/reference/get-invoice-items)、[官方余额显示](https://github.com/linode/manager/blob/develop/packages/manager/src/features/Billing/BillingPanels/BillingSummary/BillingSummary.tsx) | balance>0需付款（不能称逾期），<0可抵扣；balance_uninvoiced估算不含流量。USD；API未给付款状态，不推测paid。裁剪地址/卡/邮箱/税号，只取需要账务字段。金额缺失保留null。 |
| 防火墙 | [创建](https://techdocs.akamai.com/linode-api/reference/post-firewalls)、[规则](https://techdocs.akamai.com/linode-api/reference/put-firewall-rules)、[设备](https://techdocs.akamai.com/linode-api/reference/post-firewall-device)、[实例替换](https://techdocs.akamai.com/linode-api/reference/put-linode-firewalls) | 双向完整规则最多25，TCP/UDP/ICMP/IPENCAP；TCP/UDP端口1–65535、IPv4/IPv6 CIDR校验。共享修改须展示全devices并将图快照纳入确认。删除有绑定FW拒绝，需先明确解绑。 |
| 新旧接口 | [创建Linode](https://techdocs.akamai.com/linode-api/reference/post-linode-instance)、[Interfaces](https://techdocs.akamai.com/linode-api/reference/get-linode-interfaces)、[接口FW](https://techdocs.akamai.com/linode-api/reference/get-linode-interface-firewalls) | 新建显式legacy_config+firewall_id=-1，不受账户默认null/缺省影响；新接口以type=linode_interface、精确interfaceID和parent实例核实device绑定。interface PUT不支持firewall_id，绝不猜测该payload。VLAN不能绑定FW。 |
| 权限 | [OAuth scope响应头](https://techdocs.akamai.com/linode-api/reference/response-headers) | 读取资源/防火墙/账户，不枚举profile/tokens。X-OAuth-Scopes只转换能力说明，缺头未知；读成功不证明写或资源IAM授权。高级事件核实还需events:read_only。 |

当前流量与监控的时间窗不同，依据[官方TransferHistory](https://github.com/linode/manager/blob/develop/packages/manager/src/features/Linodes/LinodesDetail/LinodeNetworking/NetworkingSummaryPanel/TransferHistory.tsx)：当前自然月流量走无年月transfer；历史月才使用bytes totals。Stats当前年月为过去30天，界面分别说明。

Narbe保守限制：附加Volume的resize/clone/migrate、托管LKE/placement group、跨DC VPC/VLAN、core/distributed跨类型及distributed clone拒绝，解释原因，不自动删盘、解绑卷、重置现有网络。新接口跨地区仅目标明确提供Linode Interfaces能力才允许。这些部分是本候选的保护边界，不冒充云厂商所有操作的绝对限制。

全部放行：默认新建使用明确无Cloud Firewall绑定（legacy_config、firewall_id=-1）；已有账户默认不被更改。使用Cloud Firewall时ACCEPT/ACCEPT+空双向列表覆盖全部协议、端口和IPv4/6，无DROP冲突。可选择已有FW或自定义；克隆仅调整新机。权限不足时默认拒绝无法保证的模式，只有用户明确勾选人工核实才继续“全部放行”创建，最终无法回读仍为unknown，不谎称生效。回读规则与绑定不等于实际网络连通性已验证，OS防火墙不受控制。

四云大洲采用独立显式region-ID映射，地理来源沿用 [BETA2_SOURCES.md](BETA2_SOURCES.md)；不按ap-/eu-猜测。未知ID进入其他／位置待确认；RegionContinent只用于显示，云写始终保留原始ID。
