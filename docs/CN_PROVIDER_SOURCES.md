# 自动筛选公网 IPv4：云厂商策略与证据

核实日期：2026-11-19。官方 API 研究与 HTTP 合同测试分开记录；无真实收费云写或真实受阻 IP 验收。

| 厂商 | 新建自动筛选 | 已有服务器手动换 IP | 重要边界 |
| --- | --- | --- | --- |
| AWS | 普通 EBS 自动公网 IPv4 使用 stop → stopped → start → running → 地址回读 | 同一路径；另支持 EIP 新申请并关联、已有 EIP 重新关联 | 单主 ENI/私有地址，EBS 及实例保留；EIP不因停启改变，旧EIP保留计费；特殊购买、复杂网络明确拒绝，不删已有EC2 |
| Azure | 增量创建，原 VM 上新建静态 Standard PIP并换绑NIC；新地址检测通过后逐项清理本轮旧PIP | 新建静态 PIP、更新原NIC、完整回读；旧PIP始终保留 | VM、所有磁盘、NSG、VNet、Subnet及NIC其他字段保留；自动清理只接受本轮账本、标签、地区、未引用状态准确匹配的旧PIP，响应未知不重放 |
| Linode | 优先新增 IPv4；明确配额拒绝且仍为单一普通公网地址的本轮新机可严格核实后删除重建 | legacy 使用实例IP申请；新Interfaces通过public interface全量地址更新，必要时关机并恢复原态；账号内同地区保留IP可重新分配 | 额外IP≠主IP替换，旧IP保留；额度/权限以真实API拒绝为准，unknown不触发删建。已有机不删，多IP主次不明确如实展示；特殊Volume/VPC/VLAN等不自动删除 |
| DigitalOcean | 普通 Droplet 原始 IPv4 不能原机随机更换；只删除本轮新建且无附属/Reserved绑定的准确ID并确认404后重建 | 支持Reserved IP新建绑定、已有同地区地址重新分配；提供独立创建替代实例 | 原始IPv4与Reserved服务地址分开展示，已有Droplet不删；绑定的Reserved IP当前免地址费，未绑定IPv4按官方价格计费，流量另计 |

所有换 IP 写请求在派发前都必须通过 `WithOperationRecorder` 保存操作、阶段、准确资源 ID 和关联资源账本。缺少 recorder 或保存失败时不发送新写请求。任何写结果未知时保持未确认检查点，再次调用不能重放；只读恢复不能隐式继续关机、开机、删除或新建。

新旧地址由管理端工作流比较；stop/start 或删除重建不是“必定分配不同地址”的保证。达到证据或预算边界时由工作流暂停，不能因为获取到新实例 ID 就宣称获得可用的新 IP。

## 官方依据

- [AWS Stop and start Amazon EC2 instances](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Stop_Start.html)：EBS 根卷支持 stop/start；自动分配的公网 IPv4 会释放并重新分配。
- [AWS How stop/start works](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/how-ec2-instance-stop-start-works.html)：EBS、ENI 保留；EIP 保留，附加网卡 / EIP 的特殊情况不能套用普通换 IP 假设。
- [AWS DescribeAddresses](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeAddresses.html)：按 `network-interface-id` 查询关联 EIP。
- [Azure NIC IP address settings](https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/virtual-network-network-interface-addresses)：公网 IP 资源关联到 NIC 的 IP configuration，可更换关联的公网 IP。
- [Azure Dissociate public IP](https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/remove-public-ip-address-vm)：先从 NIC IP configuration 解除关联；不能把 VM 删除当作全部附属资源及费用已经消失。
- [Azure NIC Create or Update REST](https://learn.microsoft.com/en-us/rest/api/virtualnetwork/network-interfaces/create-or-update)：NIC 更新使用 PUT，包含 DNS、转发、安全组和 IP configurations 等属性，不能只重建部分字段而丢失其他设置。
- [Azure Public IP REST](https://learn.microsoft.com/en-us/rest/api/virtualnetwork/public-ip-addresses)：Public IP 是独立资源，分别创建、查询、删除。
- [Linode Delete a Linode](https://techdocs.akamai.com/linode-api/reference/delete-linode-instance)：删除不可逆；磁盘、配置、接口删除，外部 Volume 解绑；Reserved IP 保留并继续计费。
- [Linode Get networking information](https://techdocs.akamai.com/linode-api/reference/get-linode-ips)：可读取普通 / reserved / shared / VPC 地址与地址归属。
- [Linode Reserved IPv4 July 2026](https://techdocs.akamai.com/linode-api/changelog/july-1-2026)：Reserved IP 在实例删除后仍保留，持续按小时收费。不能沿用“所有公网 IP 删除实例即释放”的旧假设。
- [DigitalOcean static address lifetime](https://docs.digitalocean.com/support/are-my-droplets-ip-addresses-static/)：普通 Droplet IPv4 在实例生命周期内保持；删除释放回地址池，没有新旧地址一定不同的保证。
- [DigitalOcean Reserved IP 列表](https://docs.digitalocean.com/reference/api/reference/reserved-ips/)：Reserved IP 是独立地址，分页返回 droplet 归属；需要 reserved_ip:read，不能通过普通主 IP 查询证明没有绑定。只查询完整列表并排除本轮目标绑定，不阻断其他 Droplet 或未绑定的 Reserved IP。
- [DigitalOcean Rebuild](https://docs.digitalocean.com/products/droplets/how-to/rebuild/)：Rebuild 保留 IP，不能用于本功能换 IP。
- [DigitalOcean associated resources API](https://docs.digitalocean.com/reference/api/reference/droplets/)：只读列出关联资源；此功能不调用危险的全量关联资源删除。

## 未验证范围与并发限制

Azure 官方 NIC PUT 合同未明确列出 If-Match 原子条件参数，当前只能把 ETag 作为额外防护，**不声称已证明 Azure 跨控制台原子 CAS**。实现不增加本地账号锁或服务器锁；在写前和写后检查实际云状态与完整快照。写入的完整 GET 表示、If-Match 行为、Standard Public IP 关联及解除速度仍须在另行授权后做真实 Azure E2E。发生无法核实的差异会停止后续删除，新建 Public IP 可能仍计费并保留在账本内。

真实 AWS stop/start、Azure Public IP 交换、Linode / DigitalOcean 删除重建，以及各云收费边界、配额和地址重复概率，均未完成真实写 E2E。Linode 新式 Interfaces 和 Reserved IP 已有原机适配与合同测试；附属 Volume 等复杂配置不自动删机。真实配额、IAM、来宾网络配置及云端执行仍需另行验证。

## 候选镜像验证与交付

本次为功能候选，不修改 beta.5 正式 Tag、GHCR 镜像或发行证据，不创建新正式 Release。正常 CI 继续使用仅 `contents: read` 权限，在原有 Ubuntu 24.04 AMD64 与 Ubuntu 24.04 ARM64 原生 Runner 中分别构建管理端和 Probe，版本标签为 `cn-candidate`，source revision 固定为该次 Git Commit SHA。

两平台分别执行 Probe 实际 HTTPS 注册、心跳、非 root / 只读根文件系统 / 无入站端口、身份持久化、重启与重建检查；通过后的双镜像离线安装包保存为 `narbe-candidate-linux-amd64/arm64`，用法见 [候选安装器](IP_CANDIDATE_INSTALL.md)。独立 Probe 镜像也会保存到 Actions Artifact：`narbe-probe-linux-amd64`、`narbe-probe-linux-arm64`。每份包含 Docker image tar、image-inspect.json、SOURCE_SHA 和 SHA256SUMS，可下载后先 `sha256sum -c SHA256SUMS` 再 `docker image load --input narbe-probe-linux-<架构>.tar`。Artifacts 保留 14 天，需要 GitHub 仓库访问权限；这不是公开 GHCR 发行。

管理端升级回归从当前正式 beta.5 固定镜像开始：`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.5@sha256:bd27a8523fb2221ab5b66778a4e9408800281dcfc6c7dfc9d543e4117afea070`。测试使用隔离 Compose 项目与 disposable Volume，不触碰 Owner 数据。CI 配置接入不代表远端 CI 已成功；最终交付以实际 Commit 对应的成功 Run 与 Artifact 为准。

## 本轮新增官方 API 依据

AWS AllocateAddress / AssociateAddress、Azure Public IP / NIC 更新的精确来源见 [IP_AWS_AZURE.md](IP_AWS_AZURE.md)；Linode legacy、新Interfaces、保留地址分配以及DO Reserved IP合同见 [IP_LINODE_DO.md](IP_LINODE_DO.md)。Linode 当前分配端点为 `POST /v4/networking/ips/assign`，不是较旧示例中的 `networking/ipv4/assign`。DO地址费依据：[Reserved IP Pricing](https://docs.digitalocean.com/products/networking/reserved-ips/details/pricing/)，不把绑定免费等同于Droplet或流量免费。
