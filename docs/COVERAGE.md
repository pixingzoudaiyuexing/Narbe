# 旧项目调查与第一阶段覆盖矩阵

调查日期：2026-10-13。此文明确区分公开文档、历史线索、Mock/合同测试、真实本地集成和真实云 E2E。

## 旧项目事实

正式参考 [cdntip/cloudpanel-docker 固定 README](https://github.com/cdntip/cloudpanel-docker/blob/c6190bcac0c47f9a8724146569bbdecdfd4b0e9e/README.md)、[Compose](https://github.com/cdntip/cloudpanel-docker/blob/c6190bcac0c47f9a8724146569bbdecdfd4b0e9e/docker-compose.yaml)、[环境模板](https://github.com/cdntip/cloudpanel-docker/blob/c6190bcac0c47f9a8724146569bbdecdfd4b0e9e/.env)。仓库只有四个公开文件，无明确许可证。README 证实四云、单用户、x86-only、通过镜像发布、AWS 镜像手工更新命令与管理员管理；后端未提交。

Compose 使用 `cdntip/cloudpanel:api`、`:web`、MySQL 8.1、Redis 6.0.6、固定子网/IP、未限制的容器日志以及公网端口 28080。环境含不适合直接公网运行的调试和弱默认配置。本项目没有复用其代码、资源或默认凭据。

实际尝试官方镜像 manifest 查询，api/web 均因 `registry-1.docker.io/v2` 超时失败；标签 API 与 auth 也不可达。未下载旧镜像层，未运行旧版，未取得镜像 digest/大小，不能用 README 的 x86 声明代替 manifest 实证。

同作者 [2023 年历史 Azure 项目](https://github.com/cdntip/cloudpanel/tree/a093fd3d4b3c5ed9fbca72a8ca4080d412691976) 有账户增改删、备注/有效性、实例筛选/启停/重启/删除/创建/刷新/换 IP、镜像、后台同步等线索。它与 2024 Docker 镜像没有版本关联，不能当作该镜像实际功能证明，也没有复制其源码。

## 覆盖

| 功能 | 需求/旧版证据 | Narbe 当前行为 | 验证与缺口 |
|---|---|---|---|
| AWS/Azure/Linode/DO | README 明确 | 四个独立适配器，固定官方 API | HTTP 合同 Mock；真实云未验证 |
| 单用户登录、密码设置 | README 管理员 + Task | Argon2id、会话、改密/撤销 | 真实 API/SQLite、浏览器、Docker 验证 |
| 账号接入、备注、凭据更新/移除 | Task；历史线索 | 验证认证后的资源读取；加密；revision；retire 保留历史 | API/SQLite + 浏览器测试；各真实 IAM 未验证 |
| 实例列表、状态、公网 IP、按账号/地区查询 | Task；历史线索 | 四云适配实例/IP/status，显式地区筛选 | 四云 HTTP 合同；浏览器使用云替身 |
| 动态地区、规格、镜像 | Task；旧版 AWS 手动命令 | 动态官方目录、分页、架构/地区/最小盘筛选，执行前重查 | 合同测试含真实 Linode 日期格式；真实目录需凭据 |
| 创建 Linux / Windows（实际支持） | Task | AWS/Azure 可选 Windows，DO/Linode 不伪装 Windows 支持；厂商专属表单 | HTTP 请求/校验 + 浏览器 Mock；没有实际计费创建 |
| 开机、关机、重启、删除 | Task；历史线索 | 确认、持久任务、身份重查、异步状态 | Mock/合同/API 测试；没有真实删除 |
| SSH、子网、安全组、磁盘等辅助配置 | Task 可部署性；历史 Azure 线索 | 读取已有配置；Azure 专属受限 NSG/PIP/NIC/OS disk；账本 | 合同测试，实际网络可达性未验证 |
| 任务进度、异常、恢复 | Task | queued/running/succeeded/failed/unknown/cancelled/resolved；不重发 | SQLite reopen、并发/重复请求、错误注入、浏览器丢响应测试 |
| 多管理员 | 旧 README 有 createsuperuser | Owner 明确单用户，不实现多用户；保留单管理员密码管理 | 按本任务单用户特化，需上游在覆盖验收认可 |
| AWS 手工镜像更新 | 旧 README | 改为每次官方动态查询，无手工旧镜像导入命令 | 请求/分页合同 |
| 原版更换公网 IP | 仅历史仓库线索，未关联旧 Docker 版 | **未实现** | 必须先确认旧镜像是否真实具备及各云语义/费用，不能宣称覆盖 |
| 原版其他实例操作、设置、辅助页面 | 未能观察旧镜像 | **完整清单尚未确认** | BLOCKED：官方旧镜像 registry 不可达 |
| 后台定时账号/资源同步 | 仅历史线索 | 当前按需刷新资源、自动查询本地任务状态；无独立云资产同步定时器 | 与旧镜像是否等价未验证 |
| 默认受维护系统优先 | Task | 官方发行数据自动更新、标准/LTS/扩展/结束/未发布分级；结束/未发布默认隐藏，未知明确显示 | 生命周期解析回归及三个真实公开数据源只读验证通过 |
| 单容器、持久化、一键部署 | Task | 多阶段 scratch、非 root、Compose Volume、start/build 脚本 | 真实 Docker 和 SQLite 重建验证 |
| 日志、磁盘与清理 | Task | 日志有界，唯一专用 Builder，只清确切本次候选和明确旧 current | CLI 故障替身 + 实际 Docker 库存检查 |

所以当前不能声明“旧版全部功能覆盖”或“第一阶段最终 COMPLETE”。缺口应与完整实现、测试和 PR 一起交回 Project + Product Manager；它们不会被 Mock 成功或正式审核待安排所掩盖。

## 官方 API 依据

* [AWS Query](https://docs.aws.amazon.com/ec2/latest/devguide/Query-Requests.html)、[RunInstances](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_RunInstances.html)、[DescribeImages](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeImages.html)、[幂等](https://docs.aws.amazon.com/ec2/latest/devguide/ec2-api-idempotency.html)。签名使用 AWS 官方 Go SDK signer。
* [Azure VM API](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/create-or-update?view=rest-compute-2024-07-01)、[LRO](https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/async-operations)、[镜像](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machine-images/get?view=rest-compute-2024-03-01)、[状态/计费](https://learn.microsoft.com/en-us/azure/virtual-machines/states-billing)。DiskRP 与 VM API 版本分开。
* [Linode API](https://techdocs.akamai.com/linode-api/reference/api-summary)、[创建](https://techdocs.akamai.com/linode-api/reference/post-linode-instance)、[Images](https://techdocs.akamai.com/cloud-computing/docs/images)、[无密码创建变更](https://techdocs.akamai.com/linode-api/changelog/jun-3-2026-password-less-linode-support)。
* [DO Droplets](https://docs.digitalocean.com/products/droplets/reference/api/droplets/)、[Actions](https://docs.digitalocean.com/reference/api/reference/droplet-actions/)、[Images](https://docs.digitalocean.com/reference/api/reference/images/)、[Windows 边界](https://docs.digitalocean.com/support/can-i-use-windows-on-a-droplet/)。

生命周期资料：[distro-info-data 分发说明](https://salsa.debian.org/debian/distro-info-data/-/raw/main/debian/README.Debian)、[Ubuntu CSV](https://debian.pages.debian.net/distro-info-data/ubuntu.csv)、[Debian CSV](https://debian.pages.debian.net/distro-info-data/debian.csv)、[Windows Server 官方 Markdown](https://learn.microsoft.com/en-us/windows/release-health/windows-server-release-info?accept=text/markdown)。微软导出 XLSX 的 Windows Server 2025 日期与当前发布页不一致，因此使用当前发布信息。
