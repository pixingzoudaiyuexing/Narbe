# beta.3 候选交付：Linode高级管理与四云大洲分组

日期：2026-10-15。High Risk，Formal Independent Review REQUIRED / PENDING。实现及候选验证完成后交回Project + Product Manager安排独立审核；不发布beta.3，不合并main，不更改Owner测试服务器。已发布beta.2 Tag/镜像保留。

最终应用与镜像源码提交：`8296368821efb8eba2add544a9eb60f5324a74cc`。主要功能在c97d0fd，d5d627a仅修gofmt漏项；收尾修正新增同名账号权限检查必须使用保存接口返回ID，以及真实全0监控的实际峰值不能被绘图最小尺度1替代；两项均有浏览器先红后绿证明。最终审核仍应以本轮交付HEAD全部diff为准，不沿用beta.2的PASS。

## 九项覆盖矩阵

| 功能 | 实际API实现 | 用户操作及安全 | 自动化证据 |
|---|---|---|---|
| 重装系统 | POST实例/rebuild，动态images/keys目录，root_pass或authorized_keys | 显示当前系统/规格/地区/ID，警告删除disk/config，影响勾选+名称确认，保留实例；完成刷新系统 | SSH-only、payload、破坏确认、旧镜像/磁盘变动拒绝、异步finished、浏览器 |
| 升降配 | POST实例/resize，cold且禁止auto_disk_resize | 当前→目标CPU/内存/磁盘/价格；实际分配MB≤目标；附加卷/特殊管理关系拒绝，无自动删盘 | 1MB边界、禁止暗改disk、pending/分页、浏览器价格与配置差异 |
| root密码 | POST实例/disks/{disk}/password | 要求先offline，不自动关/开机；明确ext3/ext4系统盘，多盘不猜第一个；本地强度+云检查 | running拒绝、swap/非所属磁盘拒绝、密码加密入库/不在任务返回、payload |
| 克隆新机 | POST源/clone，独立新ID，配置/引用disk及额外disk | 源先关机保护一致性、明确新增计费；不覆盖原机；新机完成后设置所选FW，保持关机 | 新旧ID、串克隆事件、失回复仅1POST、账本失败不继续、源FW不改、浏览器 |
| 数据中心迁移 | POST实例/migrate region/type=cold/upgrade=false | 与宿主迁移区分；动态能力/套餐验证，说明停机/IP变化/旧备份/DNS；复杂网络拒绝 | 正确payload、VPC/卷/core-distributed阻断、GET新地区/IP、浏览器 |
| 流量 | 当前/历史实例transfer + account/transfer | bytes、GiB（官方GB）及月份/查询时间；实例贡献额与共享池分开；缺字段未知 | 单位/当前月路径/历史/池读取403隔离、浏览器 |
| 运行监控 | 当前24h/月历史Stats | 真实SVG时序CPU、disk I/O、IPv4/v6；时间/单位/采样来源；无数据不伪造0 | epoch毫秒、间隔不规则、CPU>100、null采样拒绝、历史/空数据/迟响应 |
| 账单 | Account、Invoices、Invoice/Items分页 | 正余额待付款、负余额抵扣；未出账估算不含流量；付款状态未提供；无支付接口 | PII裁剪、unknown金额非0、unit_price数字/字符串、分页、权限403、迟响应 |
| 云防火墙 | Firewalls CRUD/rules/devices、legacy实例替换/newinterface精准device | 默认新机明确全部放行；已有/custom可选；共享修改列全影响；绑定/配置快照，删除有绑定拒绝 | IPv4/v6/协议/端口、默认null覆盖防护、精准parent、部分写checkpoint、未知恢复目标核验 |

API端点、官方来源及差异判断详见 [BETA3_SOURCES.md](BETA3_SOURCES.md)。全部功能均有后端实现；高级入口只对Linode开放，其他三云仅新增地区分组，原生命周期仍保留。

## 安全及资源边界

沿用session/Origin/CSRF、账号revision、一次性确认摘要及固定请求ID；advanced使用专用类型和每动作字段白名单，不当作简单start/stop。配置/磁盘/接口/卷/锁/托管关系指纹与FW规则/共享devices图进入确认，worker实时重新校验。新写受queued5分钟首派发保护；重启取消queued、running变unknown，不重放。

部分云写后立即持久保存资源及成功步骤；数据库保存失败停止后续写，未知不得盲目重试或自动清理收费资源。clone新ID必须不同于源且目标名称/地区/套餐再次核实；完成事件必须同时匹配源与secondary目标，不拿另一克隆完成事件证明成功。防火墙写前保存目标规则/状态，回读失败后仍能严格只读核实，缺证据不能误报成功。

只读新API最多2并发、每请求30秒；分页资源5000上限，FW管理200个上限，事件最多20×500并检查重复ID、监控每序列20000点/图表抽样最多约600点、账单按25条翻页。不引入Go/Node依赖、额外服务或数据库，不修改schema。

新建明确`legacy_config`、`firewall_id=-1`，不是省略/null。不改账户全局默认、不改旧服务器；新接口public/VPC按所选interfaceID和parent实例绑定，VLAN不支持。重装/resize/migrate不重置FW。全放行只指云防火墙，不控制OS防火墙、监听服务或实际连通性。自定义双向ACCEPT空规则覆盖所有协议且无DROP。

## 测试与内部复核

已执行完整Go race、go vet、四云HTTP合同、真实SQLite/认证/CSRF/幂等/恢复与原六项Finding回归。实际Chromium包含原Go API+SQLite/云替身完整流程、beta.2全区域回归以及beta.3九项新UI。新UI14个模拟确认/任务、222个模拟GET（另1次模拟账号保存），所有云API写均为HTTP transport替身，真实云写0。任务从queued→succeeded后自动刷新账号inventory，重装系统/resize套餐/迁移地区与IP显示新值已测试。

定向破坏验证均先红后绿：去掉破坏性ack、把显式firewall_id=-1改为null、反转disk容量判断、忽略clone secondaryID、忽略目标FW规则核对。额外捕获并修复：跨动作pending遗漏、事件跨页与重复ID、镜像字符串secondaryID、部分FW创建/绑定后的非unknown错误、invoice非法分页与缺字段、Stats null被强转0、UI目录/账单/监控迟响应、同名账号权限检查串号及全0监控峰值误标1。测试恢复后通过，未保留故意破坏代码。

内部两视角只读复核报告的事件解析/串克隆、FW恢复目标/延迟existing FW核实、保护关系指纹问题已修。内部复核与agy不是Formal Independent Review。agy实际3次：Design2（1次timeout参数缺单位被CLI拒绝，1次120s超时）、Embedded1（120s超时），无外部结论；没有换模型或绕过权限。

复跑：`go test -race ./... -count=1`、`go vet ./...`、`NARBE_BROWSER_TEST=1 go test ./internal/app -run '^TestBrowser$' -count=1 -v -timeout=240s`、`node scripts/format-test.mjs`、`python3 scripts/test_build.py`。govulncheck已运行0个可达漏洞，1个required module无调用路径advisory；不能声称依赖数据库没有任何告警。

本机ARM64 Docker及公开beta.2→候选升级已验证；同名账号权限修复后更新最终镜像并重验，确切ID/CI/清理结果见下方收尾证据。这是候选运行验证，不是beta.3发行验证。首次源码CI在gofmt检查失败，修正空白后两条CI通过；没有把首次失败隐去。最后同名账号修复有新浏览器先红后绿和独立CI。

CI仅在现有固定SHA Actions与contents:read基础上加入新浏览器脚本格式检查，并将升级基线更新为beta.2 pinned digest；无新Action/权限/凭据。发布workflow和release/可执行文件保持已发布beta.2状态；本轮未触发发布，不设置beta.3审核变量。正式PASS后必须另行建立匹配beta.3的SHA门禁与不可变版本发布记录，不能把旧beta.2批准变量用于新应用。

## 真实API与Mock分级

本轮没有执行真实重装/resize/password/clone/migrate/FW修改或收费创建/删除。当前Netcatty只暴露其他主机，没有Owner Narbe测试机的已保存目标或活动会话；未连接无关主机，未读取真实Token/数据库/key，新增只读联调NOT VERIFIED。beta.2历史Linode6实例只读证据不升级成本轮九项真实验收。

已验证的是官方文档/当前schema、严格HTTP合同、真实浏览器/SQLite与Docker候选。真实Linode IAM、特殊磁盘/接口、配额/容量、事件时序、API发布物差异仍需经授权实测；其他三云真实创建/删除和旧CloudPanel完整覆盖缺口继续存在。

## 截图（实际浏览器，全部为测试替身数据）

| 范围 | 桌面 | 手机 |
|---|---|---|
| 四云大洲 | [AWS](images/beta3/beta3-aws-create-groups.png) · [Azure](images/beta3/beta3-azure-create-groups.png) · [Linode](images/beta3/beta3-linode-create-groups.png) · [DO](images/beta3/beta3-digitalocean-create-groups.png) | [服务器](images/beta3/beta3-servers-mobile.png) |
| 高级管理 | [管理](images/beta3/beta3-management.png) · [重装](images/beta3/beta3-rebuild.png) · [配置](images/beta3/beta3-resize.png) · [克隆](images/beta3/beta3-clone.png) · [迁移](images/beta3/beta3-migrate.png) | [管理](images/beta3/beta3-management-mobile.png) · [底部](images/beta3/beta3-management-mobile-bottom.png) |
| 流量/监控 | [流量](images/beta3/beta3-transfer.png) · [监控](images/beta3/beta3-monitoring.png) · [无数据](images/beta3/beta3-monitoring-empty.png) | [流量](images/beta3/beta3-transfer-mobile.png) · [监控](images/beta3/beta3-monitoring-mobile.png) · [底部](images/beta3/beta3-monitoring-mobile-bottom.png) |
| 账单/FW | [账单](images/beta3/beta3-billing.png) · [FW](images/beta3/beta3-firewalls.png) · [共享规则](images/beta3/beta3-firewall-shared-rules.png) · [legacy](images/beta3/beta3-firewall-bind-legacy_config.png) · [新接口](images/beta3/beta3-firewall-bind-linode.png) | [账单](images/beta3/beta3-billing-mobile.png) · [FW](images/beta3/beta3-firewalls-mobile.png) · [底部](images/beta3/beta3-firewalls-mobile-bottom.png) |

agy Specialist Calls: 3
- Research Scout: 0
- Root-Cause Challenger: 0
- Design Challenger: 2
- Test Designer: 0
- Embedded Reviewer: 1

Embedded Review Calls:
1 / 0 completed / 1 failed / 0 re-review

## 最终候选构建与升级证据

应用源码为 `8296368821efb8eba2add544a9eb60f5324a74cc`。本机一次主功能构建后，因同名账号权限及真实零峰值两项实际浏览器缺陷分别更新镜像；每次构建验证后删除专用Builder/缓存和被替代候选，非无意义重复构建。最终本地镜像 `narbe/local:current` ID `sha256:08fff234790ad9f219311271f781ac7bb8fbb2a2d44bede734c4ac9761e34e63`，压缩内容inspect.Size=5,491,852 bytes，Docker磁盘约18.7 MB。scratch/non-root65532，无Go编译器、Node或开发依赖；版本label为本地dev，不冒充公开发行镜像。

最终镜像使用scripts/test_runtime.py验证首次密码登录、改密、重启/重建持久化、health、只读root/cap-drop/no-new-privileges/日志5m×3；scripts/test_upgrade.py以公开beta.2 pinned digest为旧镜像，停机私有副本写入加密非真实测试账号，升级同一专用Volume并比较master.key、settings/密码及非空credentials行，之后强制重建再次登录。源数据Volume、Owner服务器和真实云资源均未接触。

最终原生Linux AMD64 [Push CI](https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/38616192260) 与 [PR CI](https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/38616200250) 均success，源码为8296368完整SHA。包含完整race/vet/漏洞/四云合同/三个浏览器流程、Docker构建/运行与beta.2→候选实际升级；本机原生Linux ARM64最终同等运行及升级复验PASS。不存在已知未解决回归；此结论限定在合同、浏览器/SQLite及Docker层，不能替代真实云E2E或Formal审核。

最终运行二进制12,973,717 bytes，SHA256 `82a5b63a1095e8d3ee8e031c2f7a183f01696d0380b8b9128085ee540039a0d3`；实际二进制govulncheck 0可达，1个required module advisory无调用路径。

清理实读：被替代的两个候选ID均由脚本定向回收；最后无临时容器、Volume或构建缓存，专用Builder和无引用的本次BuildKit工具镜像约510 MB已删除。保留当前候选、beta.2与beta.1回滚镜像，其他项目Rust镜像未动。Docker总占用约2.286 GB（含其他项目），宿主可用约14 GiB。临时提取二进制、OpenAPI中间下载、重复调试文件已清理，保留脱敏最终日志和红绿证据；无全局prune、无正式数据删除。

最后证据提交只含README/docs/截图，使用skip ci避免文档重新触发全量镜像构建；应用、脚本与CI配置和上述已通过源码完全相同。工作区既有两份未跟踪.DS_Store保留。beta.2 index仍为 `sha256:e9817422e60506d60e1988f22caf7ce4fdd49e9dc350cfb3b11f41aa69870fd6`；main仍为 `529de7a980d1e0c182ae59a8b282eb6c91148494`；beta.3 registry tag已匿名确认不存在。
