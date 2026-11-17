# 自动筛选公网 IPv4：云厂商策略与证据

核实日期：2026-11-15。仅检索官方 API / 产品文档，开发验证使用本地 HTTP 替身，没有收费云写，也没有把 HTTP 替身等同于云端验收。

| 厂商 | 已实现路径 | 安全边界 / 明确拒绝 |
| --- | --- | --- |
| AWS | 原实例 EBS stop → 确认 stopped → 再次核实身份及配置 → start → 确认 running / 公网 IPv4 | 本轮 ClientToken 与 `narbe-job` 标签、准确实例 ID、账号、地区、AMI、规格、根磁盘、子网、密钥、初始化数据一致；仅单 ENI 单私有 IP；DescribeAddresses 证明 ENI 无 EIP。instance-store、EIP、复杂网络或发生外部修改时暂停，不自动删除 VM。 |
| Azure | 新建独立 Standard / Static IPv4 → 确认 → 精确替换单一 NIC IP configuration 的 Public IP → 核实 → 仅删除已解绑的本轮旧 Public IP → 确认 404 | VM、系统盘、NIC、NSG、订阅和资源组全部按本轮创建账本及标签核实。保留完整 NIC GET 表示，写前重读、发送观测 ETag 的 If-Match，写后比较唯一允许变化。复杂拓扑拒绝，不删 VM、磁盘、NIC、NSG。全部旧新 Public IP 先进入持久化账本。 |
| Linode | 删除本轮准确实例 ID → GET 404 确认 → 上层工作流另行创建 | 仅本轮标准 legacy_config 新实例、单一普通非 Reserved 公网 IPv4，无附属 Volume、VPC/VLAN、共享 IP、锁、托管集群或新接口代际。API 未返回 `reserved:false` 不能自动删除。任务自建独立防火墙则暂停，避免每轮遗留资源；已有防火墙不被删除。 |
| DigitalOcean | 删除本轮准确 Droplet ID → GET 404 确认 → 上层工作流另行创建 | 本轮标签、ID、地区、规格和镜像必须一致；检查附属资源、Volume 与 Reserved IP（完整分页读取，核对每个地址的 droplet.id，不以主 IPv4 查询 404 代替）；缺少读取权限、绑定字段缺失或复杂/未知情况暂停。不使用 Rebuild 换 IP，不使用 destroy-all 端点。 |

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

真实 AWS stop/start、Azure Public IP 交换、Linode / DigitalOcean 删除重建，以及各云收费边界、配额和地址重复概率，均未完成真实写 E2E。Linode 新式 Interfaces / Reserved IP 与附属 Volume 等复杂配置明确失败关闭，未伪装为支持。

## 候选镜像验证与交付

本次为功能候选，不修改 beta.5 正式 Tag、GHCR 镜像或发行证据，不创建新正式 Release。正常 CI 继续使用仅 `contents: read` 权限，在原有 Ubuntu 24.04 AMD64 与 Ubuntu 24.04 ARM64 原生 Runner 中分别构建管理端和 Probe，版本标签为 `cn-candidate`，source revision 固定为该次 Git Commit SHA。

两平台分别执行 Probe 实际 HTTPS 注册、心跳、非 root / 只读根文件系统 / 无入站端口、身份持久化、重启与重建检查；仅通过的原生 Probe 镜像会保存到 Actions Artifact：`narbe-probe-linux-amd64`、`narbe-probe-linux-arm64`。每份包含 Docker image tar、image-inspect.json、SOURCE_SHA 和 SHA256SUMS，可下载后先 `sha256sum -c SHA256SUMS` 再 `docker image load --input narbe-probe-linux-<架构>.tar`。Artifacts 保留 14 天，需要 GitHub 仓库访问权限；这不是公开 GHCR 发行。

管理端升级回归从当前正式 beta.5 固定镜像开始：`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.5@sha256:bd27a8523fb2221ab5b66778a4e9408800281dcfc6c7dfc9d543e4117afea070`。测试使用隔离 Compose 项目与 disposable Volume，不触碰 Owner 数据。CI 配置接入不代表远端 CI 已成功；最终交付以实际 Commit 对应的成功 Run 与 Artifact 为准。
