# beta.6 发行记录与验证边界

## 授权与状态

Owner 于 2026-11-20 明确要求“发布新的测试版本”，目标为 `v0.1.0-beta.6`。**已获 Owner 授权，发行执行中；是否已发布以实际 Release、成功 Actions、公开镜像和可下载附件为准。**

发行模式为 `owner-authorized-test-release`，`manualIndependentReview=not-performed-for-this-candidate`。本次没有新的正式人工独立复审结果，不宣称 Reviewer PASS；beta.5 的历史免人工复审授权不作为本次新授权。工程自动化验证与实际来源校验仍是必须通过的门禁。

## 固定来源

- 已验证功能候选：`8d36215df29d2c1bb854c61afcc00d4582c7fc8f`，来自 Draft PR #3。
- 历史 beta.5 发布源码：`80d326fa5ddbd777709a023825d171d3fd69e0ff`，不改其 Tag、镜像、附件或发行证据。
- 升级起点：`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.5@sha256:bd27a8523fb2221ab5b66778a4e9408800281dcfc6c7dfc9d543e4117afea070`。
- 正式发行 SHA 必须在发行证据中固定，验证是候选基线的后继，且差异只在本次明确的发行配置、版本和说明范围。构建输入、忽略/未跟踪文件与固定源码不一致时失败关闭。
- Workflow 必须核验固定授权源码与同 SHA 的成功 CI；镜像推送唯一获得 `packages: write` 的 Job 依赖前置成功。不会使用缺失旧变量来意外发行历史版本。

本记录不能代替执行日志或给未完成的 Job 记 PASS；最终 `release-evidence.json` 才记录实际 SHA、Run URL 和不可变 Digest。

## 产物和公开获取

- 管理端：`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.6`。
- Probe：`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.6-probe`，与管理端共用已公开的 Narbe Package 权限，但为独立镜像。
- 两者均提供 Linux AMD64/ARM64 原生构建、启动、健康和公开回拉验证，并通过各自的 Index Digest 固定；不是 latest。
- GitHub Pre-release：`Narbe v0.1.0-beta.6`，Tag 指向正式工作流实际 Source SHA。
- 公开附件：`narbe-v0.1.0-beta.6-install.tar.gz`、`SHA256SUMS`、`release-evidence.json`。
- 安装包含管理端 Compose/安装器、`image.env`、Probe 的 `probe-compose.yaml`/`probe-image.env`/`install-probe.sh`、版本、安装说明和内部发行记录。
- 必须实际执行无需登录 GitHub 的附件下载与 SHA-256 校验、无需 GHCR 登录的镜像拉取。Actions Artifact 仅供构建内部交换，不是用户日常升级入口。

[安装、升级、备份与回滚](../release/README.md)及[中文版本说明](../release/NOTES.md)是本版用户入口。候选专用离线安装器和旧候选文档仍保留历史证据，不作为公开 beta.6 的推荐安装方式。

## 功能范围

1. 创建时国内 IP 自动筛选：准备检查、国内/海外同目标对照、协议/端口/HTTP 挑战、连续阈值、尝试/时间/预算上限、停止、每轮资源和证据。
2. 已有服务器检测与四云手动换 IP：AWS 动态地址/EIP，Azure 原 NIC 静态 Public IP，Linode legacy/新式 Interfaces/保留地址，DigitalOcean Reserved IP。不会自动删已有 VM。
3. Probe：中文管理、实际出口、一次性注册、身份持久化、出站 HTTPS、严格公网目标和端口白名单、任务租约、防重放、停用/删除、在线和近期统计。
4. SQLite 定时阶段调度不占用原四个 Worker；保留无账号/服务器业务锁、请求幂等、固定凭据/出口 revision、未知写不重放。

四云 API 依据及特殊网络限制见 [CN_PROVIDER_SOURCES.md](CN_PROVIDER_SOURCES.md)、[IP_AWS_AZURE.md](IP_AWS_AZURE.md)、[IP_LINODE_DO.md](IP_LINODE_DO.md)。

## 升级与环境保护

必须在隔离 Compose 项目与 disposable Volume 中，以正式 beta.5 固定镜像建初始数据，再使用最终公开 beta.6 Digest 升级。检查数据库、master.key、原管理员密码、加密云账号和网络出口设置保留，项目/Volume/端口/Origin 不变；重启与强制重建后仍可登录。

Owner 只获提供升级命令，不会被自动部署。命令先核实实际安装目录、容器、Compose 项目和数据 Volume，拒绝未知自定义覆盖；停机后备份完整数据及配置，只替换 `image.env`。不运行 `down -v`，不动唯一 master.key，不清空任务。

旧版不认识新增工作流与探针状态；保留 beta.5 镜像只是保留二进制回滚能力，不证明 beta.6 数据可无损降级。恢复须依赖完整升级前备份，先在隔离 Volume 核实，且要人工核实升级后已发生的云操作，不能假定恢复数据库会撤销云资源。

## 证据必须分别成立

- 固定源码 CI 成功：race、vet、govulncheck、四云/代理/任务合同、桌面和手机浏览器、来源及安装包测试。
- 原生 AMD64 与 ARM64 管理端/Probe 构建、运行和健康成功。
- GHCR 推送成功，四个架构 Manifest 与两个 Index Digest 可查询，标签和 revision 一致。
- 公开未登录 GHCR 回拉并运行成功。
- 最终管理端镜像的 beta.5→beta.6 持久化升级成功。
- 正式安装包来自成功 Workflow；公开 Release 和附件创建成功；匿名下载 SHA-256 校验通过。
- Docker 清理有精确范围和实际记录，保留 beta.5 回滚镜像与其他项目资源，不全局 prune。

任一项失败不能宣称全部 RELEASED；`skipped` 不能冒充通过。最终证据不包含凭据、数据库、master.key 或原始密码。

## 尚未覆盖

没有真实收费云创建、删除、停启、换 IP、自动改密 E2E；没有真实受阻国内 IP 的受控对照联调；没有真实生产代理 IAM、私人 StackScript、Linode 新式 Interfaces 来宾网络和四云配额/云写时延验收。Azure NIC 原子条件写行为尚待真实验证。macOS Docker 与 Linux Docker 的出口差异测试不构成国内目标路由保证。

成本是估算预留，停止不代表资源删除或费用终止。额外/Reserved/EIP 不代表原主 IP 改变；Azure 手动换 IP 保留旧 PIP 并提示持续费用。产品处于测试预发布阶段，不宣称生产稳定，不覆盖原 CloudPanel 全功能证据缺口。
