# 国内 IP 完整功能候选交付与复核范围

开发基线：正式 beta.5 / `80d326fa5ddbd777709a023825d171d3fd69e0ff`。本轮包含新建自动筛选、已有服务器手动检测及四云原机地址管理、独立出站 HTTPS Probe、中文桌面与手机页面和可安装双架构候选；没有对正式 Tag、GHCR、Release、Owner 部署或 main 执行变更。沿用 Draft PR #3，不把内部审查等同于 Formal Independent Review。

## 组件与安全边界

- `internal/app/connectivity_scheduler.go`：SQLite 到期短步骤 CAS 领取，云写前持久化意图，预算和次数预留；等待端口、探针或云状态不占原四个 Worker，也不占云步骤槽。
- `internal/app/connectivity_async.go`：国内/海外探针租约与独立有界海外检测执行器。同目标固定 IPv4、端口、协议、Host/SNI、证书策略及预期响应；探针离线、TLS/HTTP配置错误和海外不通不触发删机。
- `internal/app/ip.go`：已有资源实时归属核实、IP查询、只读检测幂等；手动换 IP 共用原有确认/CSRF/Session/账号和网络 revision。手动工作流在单个事务中以正确类型和阶段写入，不能以通用创建流程短暂暴露。
- `internal/cloud/ip*.go`：四云共用增量接口；每次推进至多一笔云写，恢复接口只读。Azure 新机创建也按 PIP、NSG、NIC、VM 逐步推进。自动 Azure 新地址通过后持久化队列，逐个清理已核实本轮旧PIP；手动既有PIP保留。
- `internal/store/connectivity.go`：幂等新增表和列，`connectivity_schema_version=2`。使用原密钥和 Volume，不改既有凭据。升级 fixture 不调用候选 `Store.Open`，必须由候选容器首次执行实际迁移，并精确检查仅新增允许版本键和表列。
- `internal/probe`、`cmd/narbe-probe`、`Dockerfile.probe`：无云密钥、主动HTTPS控制、固定公网目标、防内网/Metadata扫描、证书校验、有界并发及响应、持久化身份/租约去重、断线结果重传。
- `candidate/`：分别原生构建验证后的 manager + Probe 双镜像离线候选，内外层 SHA256、精确源码 revision、Docker archive 及加载后 ID 验证。支持隔离全新安装和核实原 Compose/Volume 后的 beta.5 升级，不构建正式 Release。

## 已加覆盖与内部复核

HTTP 合同覆盖 AWS 动态地址与 EIP、Azure PIP/NIC/盘及旧PIP清理、Linode legacy 与 Interfaces/保留地址/配额/明确拒绝后恢复原运行态、DO Reserved IP。应用测试覆盖同账号并发、多任务探针等待不占槽、首次写证据时效、创建/删除丢响应不重放、手动初始化原子性、准确资源检测、幂等、停止、重启、费用及尝试上限。

桌面/手机浏览器覆盖真实Go HTTP界面替身；另有 `TestBrowserIPManagementRealBackend` 穿过实际登录、确认、任务队列和SQLite调度，仅云端效果使用隔离适配器替身。图片由测试生成到 `test-results/`，交付选图见 `docs/images/full-ip/`。不能将这些图片或替身结果解释为云端收费操作成功。

全量 race、vet、govulncheck、浏览器、发行门禁/安装包拒绝测试和原生双架构 Docker/升级的最终裁决，以交付报告中**精确提交对应**的实际日志、CI Run及Artifact为准。普通功能/合同测试与旧版回归仍保留，不通过跳过测试换取绿色CI。

内部只读复核发现并修复了手动初始化事务窗口、首次IP云写证据时效、升级测试提前迁移、多轮Azure旧PIP遗漏，以及Linode接口明确拒绝后的运行态恢复。agy Design Challenger 一次调用因服务地域限制失败，未取得结论；没有将其记为审核PASS。

## 限制与后续正式审核

没有真实收费云创建、停止、删机、换IP、Linode改密或Rebuild。尚无Owner授权的国内Probe与四云真实目标的完整联调；真实IAM、配额、附加IP来宾配置、账单、私人StackScript以及初始化脚本实际执行仍需另行验收。单国内探针只代表实际出口，Country.is地理证据不保证物理位置或全路径直连；Clash/TUN/VPN可能分流。

费用为用户输入的保守逐轮预留估算，不能保证真实最终账单。停止或未知时保留的实例、磁盘、旧/新公网IP可能继续收费。beta.5回滚必须恢复升级前完整数据库及master.key副本，不能仅切换旧镜像。新功能涉及自动收费/删除，作为Draft候选交回项目经理，正式发行须按Owner当时授权及适用正式审核门禁执行。

操作与矩阵：[功能说明](CN_CONNECTIVITY.md)、[四云依据](CN_PROVIDER_SOURCES.md)、[候选安装升级](IP_CANDIDATE_INSTALL.md)、[Probe](PROBE.md)。
