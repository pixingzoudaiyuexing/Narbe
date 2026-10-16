# Narbe v0.1.0-beta.5 — 云 API 网络出口与 Linode 重装初始化

**发行配置准备稿，尚未发布。** 应用正式审核 PASS WITH CONDITIONS；应用审核 SHA 固定为 `182e57759494c6fbcf6e3291d8259b524a9ea49a`，已审证据 HEAD 为 `800c5443cb5fb913e30fe71467642c6fdfa87b00`。发行配置仍 REQUIRED / PENDING，尚未获本轮正式发布授权，不设置 beta.5 审核变量。后续正式 Pre-release 须以实际成功工作流、不可变镜像 digest 和下载附件为准。

新增：

- 四云统一云 API 网络出口：默认显式服务器直连，或 HTTP / HTTPS / SOCKS5 / SOCKS5H 代理，支持有认证及无认证。
- 浏览器本地、服务器直连及代理出口 IP 分别检测，显示状态、时间及延迟；另有云 API 只读诊断，区分代理认证、TLS、云认证与权限失败。
- 代理配置使用既有 master.key 加密，密码不回显；配置 revision、queued 取消和 running 固定出口。代理故障不会自动转直连或重放结果未知的云写。
- Linode 重装复用 Shell / Cloud-init / StackScript 及动态 UDF，确认绑定脚本、镜像与版本，提交前重新核实权限。
- 重装删除磁盘及 Metadata 风险单独确认，不改变原实例云防火墙绑定、接口或归属。

重要限制：

1. 代理仅供 Narbe 后端访问 AWS、Azure、Linode、DigitalOcean API，不修改 VPS 系统代理，也没有新增浏览器本地 Agent。
2. 浏览器出口只用于比较，不代表浏览器负责云 API 请求；IP 检测成功不能证明云账号授权通过，也不能保证不同云目标共享 NAT。
3. HTTP／未加密 SOCKS 代理链路不保证代理认证传输保密，公网推荐 HTTPS 或安全隧道。Go 的 SOCKS5 / SOCKS5H 都由代理解析目标域名。
4. 真实云账号经生产代理的授权查询及第三方代理兼容性尚未完整验证，需要 Owner 实测。HTTP 合同替身和匿名 400/401 只读响应不是云授权成功。
5. 真实 Linode Rebuild 会删除所有磁盘及配置。省略新 Metadata 不保证旧 User Data 延续；需要继续使用时必须重新配置。重装后 Metadata、Cloud-init 实际执行尚未真实 E2E，API 受理不等于脚本执行成功。
6. 私人 StackScript IAM、九项 Linode 高级操作及四云真实收费写 E2E 仍有缺口；不应硬编码 Token、私钥或云密码。
7. 保留新建 Linode 默认不绑定云防火墙、全部放行的行为；已有绑定、账户默认及 OS 防火墙不自动更改。保留克隆收费、迁移停机换 IP、复杂 Volume/VPC/VLAN 的保守限制，以及 Azure VM 删除后关联磁盘／公网 IP 可能继续计费的提醒。
8. beta.4 不认识 beta.5 的云 API 代理设置。保留 beta.4 镜像不是 beta.5 数据无损回滚证明；升级前须停止服务并备份完整数据库和 master.key，回滚应依赖该升级前备份与原部署配置。

Go + SQLite、单个非 root 轻量容器，Linux AMD64 / ARM64。发行流程保留原生构建、健康运行、GHCR 按 digest 回拉安装及 beta.4 同 Volume 无损升级；准备阶段不会推送正式版本、创建 Git Tag 或 GitHub Release，不覆盖历史版本、不推 latest。

**这是测试 Pre-release，不是生产稳定版。** 不自动升级 Owner 环境；原 CloudPanel 完整功能覆盖缺口继续保留。
