# AWS 与 Azure 原机公网 IPv4 更换

`internal/cloud/ip_aws.go` 与 `ip_azure.go` 实现 `IPProvider`。调用方先读取 `IPInfo`，以其 `Snapshot`、选定方式和一次性风险确认创建 `IPOperation`；每次 `AdvanceIPChange` 最多发送一次云写。每次写前通过 `ipCheckpoint` 持久化资源身份、目标资源和 `dispatching` 记录；无法保存账本时不发送写请求。`ReconcileIPChange` 只读，响应丢失后不重放写请求。后续阶段只有在已派发写的效果得到只读证实并保存 `confirmed` 后才可启动。

## AWS

* `aws-stop-start` 仅适用运行中的普通 EBS 根盘实例、单主 ENI、单主私有 IPv4，且当前公网 IPv4 不是 EIP。停机、确认 `stopped`、开机、确认 `running` 及实际新 IP 分属不同调用。EBS 卷与实例 ID 保留；内存状态中断，自动公网 IPv4 可能重复。
* `aws-eip-allocate` 以 `narbe-job` 标签申请新的 VPC EIP，读回唯一标签及分配 ID 后关联到主 ENI。申请响应丢失时仅凭标签只读核实；标签匹配不唯一则暂停，不重复申请。
* `aws-eip-associate` 要求明确选择本账号、本地区 EIP 的 allocation ID；已关联其他实例时，必须提供匹配的 `SourceInstanceID`。若新旧 IP 相同，写前拒绝。关联可能中断来源实例的公网访问。旧 EIP 分配始终保留且可能继续计费，绝不自动释放。
* Spot、特殊购买方式、Auto Scaling 托管、实例存储根盘、复杂 ENI 或非运行态均显示不可用原因。身份快照包含账号、凭据绑定、EBS 卷、ENI、子网、镜像、密钥、标签与 user-data 哈希；阶段间配置变更使操作暂停。

## Azure

* `azure-public-ip` 适用于本订阅及配置资源组内的 VM、单 NIC、单主 IP Configuration、已关联静态 Standard IPv4 PIP。VM 不要求 `narbe-job` 标签；系统盘与数据盘、VM、NSG、子网和 NIC 的其他字段保持原样。多 NIC、多 IP Configuration、负载均衡或网关绑定显示不可用原因。
* 新 Standard 静态 PIP 使用任务 token 与 VM ID 导出的确定性资源 ID，并在首次 PUT 时写入 `narbe-job` 标签。先核实不存在，发送防御性 `If-None-Match: *`，仅接受创建响应 `201`；若响应丢失，只读核实同 ID、同标签、同地区、未关联及实际 IP。NIC PUT 前重新读取完整配置并核对 ETag，保留其余字段，仅改变目标 Public IP 引用。
* NIC PUT 后回读 VM、NIC、新旧 PIP，核实新 PIP 的反向关联、旧 PIP 已解绑以及未授权字段未变化。旧 PIP 不删除，即使它原本属于 Narbe；可能继续计费。Azure NIC REST 的 ETag 条件行为没有在此被当作云端原子锁保证，外部并发修改会以身份或完整配置回读不一致暂停。

`PrimaryChanged` 仅指云端主公网 IPv4 读回不同；不代表业务端口、DNS 或中国大陆探针已通过。调用方需独立执行网络检测。这里只经过本地 HTTP 合约测试；没有真实云账号 E2E，因此配额、IAM/RBAC、地区 SKU 和计费结果仍需真实环境验证。特别是响应丢失且云端读回长期不确定时，操作保持待核实，不能重新派发同一步写。

官方依据：

* [EC2 stop/start 与公网 IP、EBS 保留行为](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/how-ec2-instance-stop-start-works.html)
* [EC2 AssociateAddress 及旧 EIP 保留](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AssociateAddress.html)
* [EC2 AllocateAddress 标签与响应](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_AllocateAddress.html)
* [Azure 公网 IP 静态分配](https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/public-ip-addresses/)
* [Azure Public IP Create Or Update REST](https://learn.microsoft.com/en-us/rest/api/virtualnetwork/public-ip-addresses/create-or-update)
* [Azure NIC Create Or Update REST](https://learn.microsoft.com/en-us/rest/api/virtualnetwork/network-interfaces/create-or-update)
