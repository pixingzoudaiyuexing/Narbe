# 独立后续候选：任务调度、单次操作与自动 root 密码重置

2026-10-27。High Risk，**Formal Independent Review REQUIRED / PENDING**。基于 beta.5 发行候选 `e95e0b7958e31a7372ff4384e5bf5b9a7652f5a8`，独立分支 `codex/task-ux-followup`。本轮不修改原 `codex/narbe-phase1` 或 PR #1 的发行提交，不设置发行变量、不打 Tag、不推 GHCR、不合并或发布，不操作 Owner 服务器和真实云资源。

## 实际行为

| Owner 要求 | 修改与边界 |
|---|---|
| 同账号多机／同机新操作 | 去掉 submit 和账号编辑／移除中的 account_id queued/running/unknown 阻断，无替代的账号锁或服务器互斥。普通操作实时核实账号、资源 ID、名称与地区，只校验静态支持动作，由云 API 接受／拒绝；前端不再按本地瞬态 actions 隐藏操作 |
| 长任务不堵队列 | 固定 4 个 Worker 循环，单条 SQLite UPDATE RETURNING 原子领取，保留单数据库连接与短事务；不按提交无限创建 goroutine。长任务仅占一个执行槽；4 个槽全部占用时仍有界排队 |
| 请求一致性 | 每任务一次性固定 account revision、解密凭据/provider 与不可变 egress client，验证／写／轮询共用。凭据或网络修改不切换运行中任务；后续请求使用新配置，旧 queued 按原保护拒绝或取消 |
| unknown 不重放／不锁账号 | 同 ID 同载荷返回原任务，改参数 409；unknown 从不重新领取。重启 queued→cancelled、running→unknown，保留最后阶段及密文载荷，不清空任务库 |
| 云端核实 | reconcile 只调用官方只读 Poll；验证原任务加密载荷与账号 revision，不用替换后的账号证明旧任务。仅可靠完成证据才 succeeded；人工核实保留管理员密码与明确 checked，不再要求输入名称 |
| 实际 API 错误 | 保留 HTTP 状态和已知结构的官方 reason/message/code，限长并清理控制字符；敏感值、Token、密码、脚本/UDF/SSH key 原值与常见编码回显整段隐藏，保留安全分类，不返回 raw body |
| 简化确认 | 创建页面最终摘要后一次“创建服务器”；开机／关机／重启直接提交；参数操作一次提交；删除／重装保留醒目风险说明和一次明确按钮，取消所有手输名称与重复 checkbox。Session/Origin/CSRF、一次性确认摘要、资源身份及完整参数校验保留 |
| 云新密码输入 | Linode 创建／重装／重置和 Azure 新密码默认可见，统一眼睛切换。管理员／代理口令沿用原输入保护。密码不进入摘要、公开任务、日志、localStorage/sessionStorage；原密文任务及强度校验不变 |
| 任务阶段 | running 显示“执行中”及实际操作进度，不再统一显示“创建中”。unknown 展示最后阶段、只读核实和控制台建议，不阻止其他任务 |

普通操作丢响应后的失败界面只复用同一个 intent ID／载荷重试，不自动新建请求。资源列表异步重绘曾把已选 stop 恢复为默认 start，已先复现再修复：仅在同一 account/region/resource 上保留准确动作选择，不增加任务锁。

## Linode 密码自动流程与故障语义

单次提交固定同一服务器、所选 ext3/ext4 系统盘和完整配置快照。开始 running：核实→关机→唯一 shutdown 事件及 offline→重置目标盘密码→唯一 password_reset 事件完成→开机→唯一 boot 事件及 running→完成。开始 offline：直接重置并核实，保持关机。若开始是过渡态，不猜测原运行意图、不额外关／开机，把密码请求交由云 API 接受／拒绝。

每次写前先保存公开无密码账本：Stage、OriginalState、目标盘指纹、事件基线、Sent 写意图及 Confirmed 实际确认。Sent 只表示可能已派发，不证明云写完成。每一步最多 2 分钟，并服从原 25 分钟任务 context。未知结果、断网、事件缺失／多重匹配、目标变更、记录失败均停止后续风险写，不自动重复调用。明确失败记录失败阶段及云端原因；此前已完成的关机或密码变更不会被隐瞒，可能需要操作者人工恢复。

恢复 Poll 纯只读：已发 boot 可以核实最终 running；只确认关机／密码事件但未派发下一步骤时，不会替用户继续 reset/boot，也不把整个流程标成功。同机可并发提交不会改变此归属要求；多个同类事件无法唯一归属时保守 unknown。原 clone 等既有数据一致性与支持范围校验保留，没有新增产品功能或新云厂商。

官方依据：[重置磁盘密码](https://techdocs.akamai.com/linode-api/reference/post-reset-disk-password)、[关机](https://techdocs.akamai.com/linode-api/reference/post-shutdown-linode-instance)、[开机](https://techdocs.akamai.com/linode-api/reference/post-boot-linode-instance)、[事件](https://techdocs.akamai.com/linode-api/reference/get-event)。核实官方 OpenAPI 4.229.1 的最小摘录，未使用真实云凭据或写 API。

## 本地证据与回归

- 原缺陷先红：手输名称成为服务端条件、unknown 账号锁、单 Worker 队列阻塞。新增同账号 12 任务实际重叠、峰值 4、每 ID 一次云写，慢任务不阻塞其他任务；同机 API 接受／409 拒绝和具体原因；取消／并发 claim、网络 revision 变化、离线及重启既有回归保留。
- 凭据变更复现：恢复原 worker 的两次凭据读取会把任务切到新凭据，测试红；固定 snapshot 后验证、写及 Poll 均使用原凭据，新任务才使用新 revision。单 Worker 变异使同账号重叠测试红，恢复 4 Worker 后绿。
- 自动密码：8 个测试函数、约 46 个叶子场景，涵盖 running/offline/过渡态、每段拒绝／丢响应／失败或缺失事件／多个事件／错目标盘、身份变化和 recorder 失败；后续写计数严格为零，恢复 Poll 不续写。自动关机、唯一事件、目标盘事件、目标指纹四项变异红→绿。
- 错误安全：5 类官方原因先红→绿，敏感原值与 JSON/URL/Base64 变体、畸形／超限／状态分类，7 个 fuzz seed 和 62,870 次执行通过。HTTP 409 原因经安全边界进入任务错误；含秘密的详情隐藏，不以泄露换取可见性。
- 完整 Go race、vet、新旧所有 HTTP 浏览器（Go Handler/API/SQLite + 隔离云替身）、格式、actionlint、9 项构建清理测试、23 项原发行门禁及4项bundle通过。浏览器 14 次桌面／手机真实确认和入队后取消，无name额外输入、无普通动作确认弹窗、破坏性一次按钮、明文眼睛切换、空 actions 仍可操作、unknown 提示与隐私断言通过。
- 本轮仅使用 HTTP／provider 替身模拟云写，全部真实云写为 0；没有用匿名 API、HTTP 成功或 mock 计作真实关机／改密／开机验收。

### 工具链新增安全公告

本轮扫描在旧 Go 1.26.9 实际报告 [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) 的可达 crypto/x509 证书验证问题（10月21日公告）。仅本独立候选升级至官方修复 Go 1.26.10：go.mod、普通 CI、Dockerfile 及开发最低版本说明同步；原 beta.5 已审分支／发行 Workflow 不变。此构建安全差异纳入本轮正式审核。

Microsoft 官方 `1.26.10-bookworm` 多架构 Index 的实际响应 Digest 为 `sha256:7b55d3a69ad61f6980d0a9e3b60368a909000839fad24aef72c210bd5c8982c1`，与下载 manifest 原始字节 SHA-256 一致，已固定。仍为 scratch、非 root、无运行时编译器及 Node。补丁后本地完整 race/vet、HTTP浏览器重跑通过，govulncheck 0 可达漏洞；required module 保留1条无调用路径提示，没有擅自升级不相关依赖。

## 截图（测试替身，不是 Owner 或真实云操作）

- [创建桌面](images/task-ux/task-ux-create-desktop.png)／[手机](images/task-ux/task-ux-create-mobile.png)
- [删除桌面](images/task-ux/task-ux-delete-desktop.png)／[手机](images/task-ux/task-ux-delete-mobile.png)
- [重装桌面](images/task-ux/task-ux-rebuild-desktop.png)／[手机](images/task-ux/task-ux-rebuild-mobile.png)
- [密码重置桌面](images/task-ux/task-ux-password-reset-desktop.png)／[手机](images/task-ux/task-ux-password-reset-mobile.png)
- [任务与 unknown 桌面](images/task-ux/task-ux-tasks-desktop.png)／[手机](images/task-ux/task-ux-tasks-mobile.png)

截图中密码是公开测试 fixture，只存在测试表单，不是真实密码。任务列表取消项是验证期间入队后取消，unknown 项为隔离 Go HTTP 账本；实际多阶段调用与不重放由上述云 HTTP 合同测试证明，不能将截图描述为真实云执行。

## 审核与风险

内嵌 agy Embedded Reviewer 1 次 scoped PASS，覆盖 provider snapshot／Worker／密码自动阶段与恢复。3 个内部协作子任务负责云适配器、前端及安全错误，结果由主工程师运行实际工具和回归复核。它们不替代 **Formal Independent Review REQUIRED**。

仍需真实 Linode 在专门授权下核实关机／重置／开机事件形态、IAM、时延、特殊磁盘及外部并发干预；当前不执行这些写。没有服务器锁意味着同机请求可能由厂商拒绝，或者导致事件归属歧义并保持 unknown；这是 Owner 明确选择，不能伪报成功或自动重放。错误清理不保证任意未知编码均可识别，按已知字段与请求敏感值保守隐藏；主密钥和数据库同时泄漏仍可解密。默认可见密码有旁观风险，是 Owner 明确输入偏好，未弱化持久化保护。

最终提交、Draft PR、CI、双架构 Docker、持久化升级及磁盘清理的实际结果由下方最终证据段和 Engineer 报告记录。完成后停止待审，不执行发行；原 beta.5 Release Gate 继续保持原提交，不将本候选混入已审发行链。

## 最终 CI 与交付证据

可执行／完整测试基线：`3216f1e9e5151338427c7ceee5fe0090fdebf1e4`（功能提交 `a5ff9d301eb2e253869e50945aabfe7f75fe2e19`，随后补强 unknown 后同账号新任务实际执行断言）。

- [Push CI](https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/40445402068) 与 [PR CI](https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/40445405627) 均 **completed / success**；verify 24 步、arm64-runtime 12 步全部成功，未用 skipped 充当通过。
- 原生 AMD64 与原生 ARM64 实际构建、启动、健康、登录、密码与加密代理设置跨重启／容器替换持久化成功。两平台均从固定正式 beta.4 digest 实际拉取到新候选，在独立 disposable Compose Volume 升级，保留同项目／Volume、端口／Origin，逐字节核对 master.key、密码/settings 和非空加密云账号，强制重建后仍能登录。任务密文与最后阶段跨重启另由真实 SQLite 回归证明。
- ARM64 候选 inspect.Size **13,334,925B**，image ID `sha256:96dcb2a6a8071a6277010a2427bc47819450988c0b7a8d1a959212766a5bef10`，revision 为上述3216f1e；内部标签 task-ux-candidate，不是 beta.5 正式镜像或可公开安装的发行 digest。
- 本机 Docker daemon 当前未运行，**无法读取当前本机镜像／Volume 总占用**，不把历史2.344GB快照当作当前值；本轮本机镜像构建、容器及 Volume 创建均为0。CI的安装／升级只创建其自身带身份标签的临时资源，finally 定向回收。未启动本机 daemon 或进行全局 prune，原正式镜像／数据未被本任务修改。宿主检查时可用22GiB；约31.8MB本次临时截图已回收，保留10张约1.6MiB正式测试截图与小型日志。

[机器可读证据](TASK_UX_EVIDENCE.json) 记录精确来源、CI Job URL、候选镜像与保护边界。最后只补证据文档和测试截图并 `[skip ci]`，最终待审 HEAD 与3216f1e在应用、测试、脚本、依赖、Dockerfile和Workflow上逐文件相同，不重复全量构建；精确最终 HEAD 由 Engineer 报告给出。

原 PR #1 仍 Open/Draft、HEAD e95e0b7，main 仍 `529de7a980d1e0c182ae59a8b282eb6c91148494`；独立 [Draft PR #2](https://github.com/pixingzoudaiyuexing/Narbe/pull/2) 以原候选分支为 base，只包含本轮后续 Diff。不修改 release/** 或发行 Workflow、未设置任何新发行变量、不推正式镜像、不创建 Tag／Release。旧 beta.5 构建工具链未因本候选自动升级，GO-2026-6809 对旧发行锚点的影响须由上游发行决策另行评估。

agy Specialist Calls：1；Research Scout 0、Root-Cause Challenger 0、Design Challenger 0、Test Designer 0、Embedded Reviewer 1。Embedded Review Calls：1 / 1 completed / 0 failed / 0 re-review。内部 PASS 不等于正式审核，也不授权合并或上线。
