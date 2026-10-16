# beta.5 候选交付与正式审核入口

2026-10-16；High Risk。**Formal Independent Review REQUIRED / PENDING**。开发基线为已发布 beta.4 源码 `049fda6d77adcb5a4cda72ea1284662896b6d6ff`。本候选只提交原 Draft PR #1，不发布 beta.5、不合并 main、不修改 Owner 服务器或真实云资源。beta.4 的 release/VERSION、双 SHA 验证器、publish、bundle、release Workflow 与审核变量均保持，不复用其批准发布新代码。

## 用户体验与覆盖矩阵

设置页提供全局云 API 默认出口。默认“服务器直连”显式禁用环境代理；HTTP、HTTPS、SOCKS5、SOCKS5H 代理支持有认证与无认证。页面不回显密码，修改时必须保留／替换／清除；切换代理身份时禁止把原密码发给新端点。密码随设置使用现有 master.key/AES-GCM+独立 AAD 加密，重启读取失败则关闭云访问，不静默转直连。

| 流量 | 默认直连／已保存代理 | 验证 |
|---|---|---|
| 四云接入凭据校验、目录、地区、规格、镜像、价格 | 共用显式客户端 | 四云 HTTPS HTTP 合同与既有完整回归 |
| 总览、列表、详情、创建／开停重启／删除、轮询 | 共用同一客户端；写操作不自动转直连 | 原始官方 Host、AWS 签名和 Azure OAuth 合同；Worker revision 与 unknown 幂等 |
| Linode 九项高级操作、防火墙、StackScripts 与初始化 | 同一个 Provider 客户端 | 既有九项／null 防火墙／四云创建脚本浏览器回归及 Rebuild 合同 |
| Azure OAuth Token | 同一出口，限定 login.microsoftonline.com | 经代理实际 TLS 端点合同 |
| 浏览器本地 IP | 浏览器独立 HTTPS 请求 | 不传 Narbe Cookie、Referrer 或云凭据；实际公网检测 |
| 服务器直连 IP | 临时显式无代理 Transport | 固定 ipify；真实后端请求，与浏览器独立 |
| 代理 IP | 与云 API 相同工厂及代理配置 | 四协议隧道合同；受控代理真实公网检测 |
| 云 API 诊断 | 已保存出口，仅读官方目录／实例查询 | 区分网络、代理认证、TLS、401、403、授权查询通过；不以 IP 检测代替 |

SOCKS5 与 SOCKS5H 按 Go 实现均由代理解析目标域名。HTTP 代理及未加密 SOCKS 链路不保证代理认证传输保密，公网推荐 HTTPS 或安全隧道。IP 成功只证明本次测试请求的出口，不保证不同云目标共享 NAT。网络配置仅作用于 Narbe 后端云 API，不改变 VPS 系统网络，不含独立代理 Agent。

## 网络与状态安全边界

- 管理员 Session、严格 Origin、CSRF、严格 JSON 及 revision CAS。配置变更在同一 DB 事务作废确认令牌、取消 queued；已提交相同 ID 仍返回原任务，不能借换网络重复操作。
- running 固定不可变客户端及 revision，含核实、派发和轮询；新配置关闭旧空闲池、取消旧总览并丢弃晚回包。总览客户端选择及缓存入场与网络设置变更串行，避免旧客户端在清理后重新建缓存。
- 出站只允许官方 HTTPS Host/443 和固定 ipify；拒绝 URL 用户信息、Host 改写、非官方域名和重定向。原 AWS Host/path/body 签名不改变，Proxy-Authorization 不传给云端。TLS 1.2+，保持系统证书验证。
- DNS 每次 Dial 解析并校验全部答案，混入内网即拒绝，再直接拨已校验的 IP，避免二次解析重绑定。默认拒绝环回、RFC1918/ULA、link-local/Metadata、CGNAT、Azure 平台内部地址、保留及 IPv6 转换地址。
- 必须使用私网代理时，服务器管理员可明确设置 `NARBE_PROXY_ALLOW_PRIVATE=10.2.3.4:8443,[fd12::1]:1080`。只授权精确 RFC1918/ULA IP:端口；不能授权 loopback、link-local、Metadata。公网目标 Host 仍是固定白名单。Compose 可在自己的覆盖配置中加入此变量；本轮不修改发布 Compose 或 Owner 环境。不要配置任意内网 CIDR。
- 诊断后端 20 秒、2 并发、每秒一次准入、IP 响应最多 1024 字节；浏览器固定目标、10 秒、响应大小校验。无任意网站探测器、无 TLS bypass、无失败自动直连。错误及任务结果不回显代理口令、云 Token 或脚本全文。
- 代理断连后的云写不确认结果沿用 unknown，绝不自动重复创建、重装、克隆、迁移或删除。重启仍取消 queued、running 恢复 unknown，不假定写操作没有执行。

## Linode 重装初始化兼容矩阵

| 模式 | 请求与兼容性 | 保护 |
|---|---|---|
| 不提交新脚本 | 不发送 metadata / stackscript 字段 | 仍要求磁盘删除及 Metadata 单独确认；不保证旧 User Data 延续 |
| Shell | 原文一次 Base64 到 metadata.user_data | 镜像 cloud-init + 地区 Metadata，原文最多 49149 UTF-8 字节 |
| Cloud-init | 同上，要求 #cloud-config | 同一 beta.4 校验器、预算、内容指纹 |
| 已有 StackScript | stackscript_id + 动态 UDF JSON | 实时账号权限／源码版本／镜像／UDF 校验；私有镜像组合拒绝；与 Metadata 互斥 |

弹窗重用 beta.4 共享编辑器、搜索分页、动态 UDF、UTF-8/hash、草稿与镜像变化提示。删除所有磁盘及配置显著告知，额外确认加准确名称；不会通过脚本改变防火墙、接口、地区或资源身份。脚本/UDF 在原高级任务密文内；一次性确认绑定它们，修改脚本或镜像需新确认。提交、Worker、适配器再次核实新鲜权限。结果显示“提交未核实／结果未知”，不把 API 接受或开机当来宾执行成功，unknown 不重发 Rebuild。

官方依据及 AWS/Azure/DO 后续可行性见 [BETA5_SOURCES.md](BETA5_SOURCES.md)。其余三云本轮没有新重装功能。

## 证据分级和已执行验证

| 类别 | 实际结果 |
|---|---|
| HTTP 代理合同 | HTTP/HTTPS/SOCKS5/SOCKS5H × 有无认证，实际 TLS 测试端点四云请求、Azure OAuth、AWS 原始签名与代理隔离 |
| 网络安全 | DNS 公网→私网／Metadata 重绑定、混合答案、精确私网授权、TLS 失败、407、CONNECT 302/307、目标重定向、无直连降级、错误无密码 |
| 任务与加密 | 配置 CAS／保存读取／保留清除替换／密文损坏；queued 取消、确认作废、running 固定旧出口、旧总览清理、12 并发 unknown 与丢响应同 ID 不重放 |
| Rebuild HTTP 合同 | 四模式请求、单次 Unicode Base64、无额外绑定／身份字段；未确认 Metadata、超限／混用、过时版本／权限撤销／能力丢失／私有镜像拒绝；丢回包 unknown 一次写 |
| Go / 浏览器 | 完整 race、vet；HTTP 普通浏览器的真实 Go API/SQLite + 云替身；新双功能、既有九项高级、null 防火墙、四云初始化及旧流程 |
| 定向红→绿 | 移除 DNS 或官方 Host 防护、取消 queued 事务、去掉 Metadata 确认、篡改 Base64 原文、跳过 StackScript 新鲜版本检查，六项均使测试红；恢复后绿 |
| 真实只读网络 | 实际浏览器本地／后端直连／受控代理三条 IP 成功；受控代理到 AWS 匿名 DescribeRegions 400、Azure 订阅 401、Linode 公共地区 200、DO 地区 401 |

最后一行只证明官方网络到达，**未使用真实云账号凭据，不能记作四云账号授权查询或云写 E2E 通过**。代理 HTTP 合同的云端是本地 TLS 替身；高级操作浏览器和任务写是 Go 替身，无真实付费操作。

源码 govulncheck：0 可达漏洞、0 引入包漏洞；required module 1 条无调用路径提示。工作流 actionlint、格式、构建清理单元测试及历史发行门禁测试正常执行；CI 只读 permissions，固定 Actions，新增脚本浏览器自动纳入现有 TestBrowser；升级基线更新为已发布 beta.4 固定 Index Digest。

内部 Embedded Review 未得完成结论：一次参数单位错误后修正，随后唯一允许模型 API 返回地区不支持；没有换模型或绕过代理规则。Research Scout 两次：原网页读取超时，第二次使用已核实官方 OpenAPI 摘录完成分析。这些不替代本轮 Formal Independent Review。

## 截图

截图仅为候选/测试环境，既有 Owner 部署未改动。`actual-egress` 为真实公网 IP 检测，其余为 Go HTTP/SQLite 与云替身；没有伪装真实云实例。

- [三条实际出口检测，桌面](images/beta5/beta5-actual-egress-three-desktop.png) / [手机](images/beta5/beta5-actual-egress-three-mobile.png)
- [加密代理设置，桌面](images/beta5/beta5-proxy-saved-desktop.png) / [手机](images/beta5/beta5-proxy-saved-mobile.png)
- [重装 Shell，桌面](images/beta5/beta5-rebuild-shell-desktop.png) / [手机](images/beta5/beta5-rebuild-shell-mobile.png)
- [重装 Cloud-init，桌面](images/beta5/beta5-rebuild-cloud-config-desktop.png) / [手机](images/beta5/beta5-rebuild-cloud-config-mobile.png)
- [重装 StackScript，桌面](images/beta5/beta5-rebuild-stackscript-desktop.png) / [手机](images/beta5/beta5-rebuild-stackscript-mobile.png)

## 交付门禁和剩余缺口

本候选须交原 Independent Reviewer 正式审核，不能复用 beta.4 PASS。不得自行发布 beta.5。后续发行版本和双 SHA 变更另行准备审核；本次 release/**、Dockerfile 和 release Workflow 无改动。

真实四云授权账号经外部生产代理访问、各协议第三方代理兼容、实际云写超时行为、Linode 真实 Rebuild 的磁盘／防火墙／Metadata 行为、来宾脚本执行和私人 StackScript IAM 仍未真实 E2E；九项 Linode 高级操作及原 CloudPanel 完整覆盖旧缺口继续保留。内存中的 Go 字符串不能保证即时擦除，但不落明文／日志／响应；操作者仍须保护 master.key、数据库及管理端 HTTPS。
