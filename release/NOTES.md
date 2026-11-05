# Narbe v0.1.0-beta.5 — 云 API 出口、重装初始化与任务体验

**Owner 已授权发行，当前发行执行中；是否已发布以实际 GitHub Pre-release、成功工作流及公开附件为准。** 这是自用测试 Pre-release，不是生产稳定版。Owner 于 2026-11-05 明确免除本轮额外人工独立复审；本次整合与发行配置不宣称 Independent Reviewer PASS。此前的应用审核属于历史记录，不能替代本次最终源码的自动化验证。

本版包含：

- 四云统一云 API 网络出口：显式服务器直连，或 HTTP / HTTPS / SOCKS5 / SOCKS5H 代理，支持有认证及无认证。
- 浏览器本地、服务器直连及代理出口 IP 分别检测，显示状态、时间及延迟；云 API 只读诊断区分代理认证、TLS、云认证与权限失败。
- 代理配置使用既有 master.key 加密，密码不回显；配置 revision、queued 取消和 running 固定出口。代理故障不自动降级直连，不重放结果未知的云写。
- Linode 重装支持 Shell / Cloud-init / StackScript 及动态 UDF，绑定脚本、镜像和版本，提交前核实权限；明确提示 Metadata 延续风险。
- 取消账号级任务阻断，使用四个有限并发 Worker。同账号多台服务器可同时操作，同机新操作由实时云状态与云 API 接受或拒绝；没有新增服务器互斥锁。
- unknown 不锁账号、不自动重放；相同请求 ID 保持幂等。任务显示实际阶段、最后核实位置及只读核实建议，单个慢任务不阻塞整个队列。
- Linode root 密码重置一次提交完成：核实同台服务器与所选系统盘、必要时关机、核实关机、重置密码、核实事件、恢复原运行状态。原本关机则保持关机；结果未知时停止后续风险写，不盲目重试。
- 普通操作取消手工重复输入名称和多次确认；删除、重装保留醒目风险说明及一次明确确认。Session、CSRF、确认令牌、资源身份与参数一致性校验保留。
- 新云密码输入默认可见，眼睛按钮可切换隐藏；强度校验、加密任务和结果脱敏保留，不把 root 密码写入日志、公开任务或浏览器持久存储。
- Go 1.26.10 安全修复；保持 Go + SQLite、单个非 root 轻量容器，原生 Linux AMD64 / ARM64。

重要限制：

1. 代理仅供 Narbe 后端访问 AWS、Azure、Linode、DigitalOcean API，不修改 VPS 系统代理，没有新增浏览器本地 Agent。浏览器出口只用于比较，不代表浏览器负责云 API 请求。
2. IP 检测成功不能证明云账号授权通过，也不能保证不同云目标共享 NAT。真实云账号经生产代理的授权查询、第三方代理兼容性仍需 Owner 实测；HTTP 合同替身和匿名 400/401 响应不是授权成功。
3. HTTP／未加密 SOCKS 链路不保证代理认证传输保密，公网推荐 HTTPS 或安全隧道。Go 的 SOCKS5 / SOCKS5H 均由代理解析目标域名。
4. 真实 Linode Rebuild 会删除所有磁盘及配置。省略新 Metadata 不保证旧 User Data 延续；重装后 Metadata、Cloud-init 实际执行尚未真实 E2E。API 受理、开机和创建成功都不证明脚本实际成功。
5. **真实云收费写和 Linode 自动关机→改密→恢复开机 E2E 尚未完成。** 自动化使用隔离替身，不能描述为真实云写通过。事件无法唯一归属、断网或外部并发干预可能保留 unknown，并可能需要人工恢复运行状态。
6. 私人 StackScript IAM、九项 Linode 高级操作及原 CloudPanel 完整功能覆盖仍有真实云验证缺口。脚本权限较高，不应硬编码 Token、私钥或云密码；AWS Windows 执行取决于 EC2Launch／EC2Config，Azure Windows 本版不提供初始化脚本自动执行。
7. 新建 Linode 默认不绑定云防火墙、全部放行；已有绑定、账户默认及 OS 防火墙不自动更改。克隆会新增费用，迁移可能停机换 IP，复杂 Volume/VPC/VLAN 存在保守限制。Azure VM 删除后关联磁盘／公网 IP 可能继续计费。
8. **beta.4 不识别 beta.5 云 API 代理设置。保留 beta.4 镜像不是 beta.5 数据可无损回滚的证明。** 升级前须停止服务，备份完整数据 Volume、master.key 和原部署配置；回滚应依赖升级前备份并先在隔离 Volume 验证。

发行证据使用明确的 `owner-authorized-manual-review-waiver` 路径，固定实际源码 SHA、Owner 授权、同 SHA 成功 CI、原生双架构构建及运行、beta.4 同 Volume 升级、镜像 Index／平台 Digest；不把新源码 SHA 冒充已独立审核 SHA。不覆盖历史版本，不推 latest，不自动升级 Owner 环境。

安装与安全升级步骤见安装包 README；已安装用户保留实际 Compose 项目、Volume、端口、Origin、SQLite、master.key、管理员密码及加密云账号。
