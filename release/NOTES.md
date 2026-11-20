# Narbe v0.1.0-beta.6 — 国内 IP 检测、自动筛选与四云手动换 IP

**已获 Owner 授权，发行执行中；是否已发布以实际 GitHub Pre-release、成功工作流、公开 GHCR 镜像和附件为准。** 这是测试预发布版，不是生产稳定版。本轮使用 `owner-authorized-test-release`，本候选没有新的人工独立复审（`manualIndependentReview=not-performed-for-this-candidate`），不冒称 Reviewer PASS，也不复用 beta.5 的历史免审授权。

## 新增功能

- **创建时自动筛选国内可用 IP**：选择国内 Probe、海外对照、TCP/TLS/HTTP/HTTPS、端口和可选 HTTP 随机挑战；等待目标准备就绪，再进行同 IP、同端口的国内外对照。可设置连续成功/失败次数、尝试上限、等待时间、整轮期限和估算预算，随时停止并查看资源账本。
- **持久化筛选流程**：SQLite 保存每轮资源 ID、IP、费用预留、云写检查点和证据；长时间等待按定时步骤恢复，不占用原四个普通任务 Worker。无账号锁或服务器业务锁，unknown 不重放、不锁其他任务。
- **已有服务器独立检测和手动换 IP**：AWS 普通 EBS 动态地址停启，以及 EIP 新申请/关联/重新关联；Azure 在原 VM/NIC 上更换静态 Public IP；Linode 适配 legacy 与新式 Interfaces 的新增/保留 IPv4 分配；DigitalOcean 支持 Reserved IP 申请与重新分配。支持换后检测，分开展示主 IP 与额外服务地址。
- **只筛选本轮新建资源**：Linode 原地能力受限且得到明确配额失败时，严格核实本轮新机后才可删除重建；DigitalOcean 原始 IPv4 固定，只对本轮准确归属的新 Droplet 删除重建。已有服务器不会被自动删机，提供单独创建替代实例入口。
- **轻量 Probe Agent**：独立 Go 静态程序、Linux AMD64/ARM64 单容器，主动 HTTPS 连接管理端，无国内入站端口，不保存云 API Token。支持一次性注册、身份持久化、心跳、实际出口、成功率、任务租约和删除/停用。
- **公开 Docker 安装**：管理端 `ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.6`；Probe 使用同一公开 Package 的 `v0.1.0-beta.6-probe`。公开安装包中分别固定不可变 Digest，无需登录 Actions、下载 Docker 归档或在服务器编译 Go。
- 保留 beta.5 的四云 API 代理与出口诊断、Linode 重装初始化、四 Worker、自动关机/改密/恢复状态、简化确认及可见密码输入；Go 工具链更新为 1.26.11。

## 判断和费用边界

1. 不以 ICMP Ping 失败认定 IP 被墙。海外同目标也失败、尚无有效监听端口、国内探针离线、出口不符、TLS/挑战不匹配或证据不足时显示未准备好或无法判断，不自动删机。创建成功不等于初始化脚本或检测服务已成功运行；不会覆盖用户 Cloud-init/StackScript 安装检测服务。
2. 国内外连续对照属于当前证据下的“疑似国内不可达”，不构成对运营商封锁原因的证明。macOS Docker Desktop、Linux Docker bridge 和宿主 Clash/TUN/VPN 可能有不同或按目标分流的出口；以容器实际观察及独立核验为准，物理位置不能代替证据。
3. **未执行真实收费云写、真实国内受阻 IP 联调或四云实际换 IP E2E。** 合同替身、浏览器和 Docker 测试不等于云端写成功。真实配额、权限、地址分配、云时延和来宾网络仍需 Owner 另行测试。
4. 额外 IPv4、EIP、Reserved IP 不等于原主 IP 已更换；部分场景需要来宾网络配置。AWS EIP 可能收费；DigitalOcean Reserved IP 绑定与未绑定的价格不同，其他实例/流量仍可收费。旧/新地址相同会明确记录，不保证停启或重建一定取得不同地址。
5. Azure 保留 VM、磁盘、NIC、NSG、VNet 和子网；手动换 IP 保留原 Public IP，可能继续计费。自动清理仅限本轮账本和标签准确归属、已解除引用的旧地址；未知结果不重放，未确认资源继续展示并需人工检查。Azure NIC 跨控制台原子条件写未完成真实验证。
6. 成本为用户设置的估算预留，不是云厂商账单保证。停止、暂停、耗尽和未知状态不等于所有资源已删除或停止计费；界面保留现存实例及附属资源。上一次删除未知时不会盲目新建下一台收费实例。
7. 探针仅接受管理端验证的公网 IPv4 和本地端口白名单，不跟随重定向，不访问私网、Metadata 或任意扫描目标；HTTPS 控制面不可跳过证书验证。健康检查仅代表近期心跳，不代表国内可达。

## 继续保留的限制

- 真实云账号经生产代理的授权查询、第三方代理兼容性、Linode 自动改密完整云端流程、重装后 Metadata/Cloud-init 和私人 StackScript IAM 仍缺真实 E2E。代理只影响 Narbe 后端云 API，不修改 VPS 系统代理；HTTP/未加密 SOCKS 不保证代理认证保密。
- Linode 九项高级操作仍有真实云验证缺口。重装会删除磁盘及配置；克隆新增费用且要求源机关机；迁移可能停机换 IP。新建 Linode 默认全部放行云防火墙，不改已有绑定、账户默认和 OS 防火墙。
- 初始化脚本权限较高，不应硬编码 Token、密码或私钥；AWS Windows 执行取决于 EC2Launch/EC2Config，Azure Windows 本版不提供自动执行模式。Azure 删除 VM 不代表关联磁盘和网络费用结束。
- beta.5 不认识新增探针/IP 工作流状态。升级验证为 **beta.5→beta.6**，不承诺新数据能无损降级；回滚依赖停止后制作的完整升级前备份，不能仅换回旧镜像。

## 安装与升级

在公开 Release 下载 `narbe-v0.1.0-beta.6-install.tar.gz`、`SHA256SUMS` 和 `release-evidence.json` 后校验。首次安装和 beta.5 安全升级见安装包 README；探针用包内 `install-probe.sh`，管理端用 `install.sh`。保留实际 Compose 项目、Volume、SQLite、master.key、管理员密码、云账号、网络出口、端口和 Origin。升级前先核实路径/项目/Volume、停止并备份，不运行 `down -v`，不自动修改 Owner 部署。

发行证据固定已验证功能基线 `8d36215df29d2c1bb854c61afcc00d4582c7fc8f`、本次实际 Source SHA、Owner 发布授权、同源码成功 CI、管理端/Probe 双架构运行与公开回拉、beta.5 隔离 Volume 升级、各镜像 Index/平台 Digest 和 Actions URL。不覆盖历史 Tag/镜像，不推 latest，不合并 main。
