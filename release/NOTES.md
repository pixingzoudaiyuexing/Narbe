# Narbe v0.1.0-beta.3 — Linode高级管理与四云地区分组

测试预发布说明；应用基线 `4f1c9cc2a7c8e099ab3c375548c48e691d87c72d` 已正式审核 PASS WITH CONDITIONS。发行配置正在针对性复审，批准前不创建beta.3 Tag或推送正式GHCR版本。最终发布后，release-evidence.json记录实际发布提交、应用审核SHA、发行审核SHA和不可变digest。beta.1/beta.2保留，不推送latest。

本版新增：

- Linode重装系统，动态选择镜像及密码/SSH登录方式。
- Linode升级或降低配置，显示CPU、内存、磁盘与价格差异。
- Linode指定系统磁盘root密码重置。
- Linode克隆为新的服务器。
- Linode跨数据中心迁移。
- Linode当月/历史流量统计，区分实例贡献额和账户共享池。
- Linode运行监控：CPU、磁盘I/O、IPv4/IPv6真实时序图与历史查询。
- Linode账户余额、未出账费用估算及历史账单查询，无支付功能。
- Linode云防火墙查看、创建、规则/状态修改、删除及实例/新接口绑定。
- AWS、Azure、Linode、DigitalOcean地区按大洲分组，保留中文名称和原始ID搜索。

Go + SQLite、单个非root轻量容器，Linux AMD64/ARM64原生构建和镜像回拉安装。原有认证、CSRF、确认、幂等、queued有效期与running/unknown防重复执行机制保留。

需要了解的影响：

- 新建Linode默认明确不绑定云防火墙，全部放行；不会更改账户全局默认或已有服务器，不代表操作系统防火墙已关闭。已有/自定义防火墙可选；无法核实生效时明确待人工核实。
- 重装系统会清除原本地系统磁盘数据及配置，请先备份。
- 克隆会新增收费实例；当前实现要求源实例先关机，新机保持关机，需手动开机。
- 数据中心迁移可能停机并更换公网IP，旧备份不迁移；Narbe不自动更新外部DNS。
- 复杂Volume、VPC/VLAN、托管关系等存在保守限制，不自动删盘或脱离网络。
- 九项高级功能尚未经过真实Linode云端完整E2E；合同/Mock、浏览器及Docker成功不代表真实云写验收。
- 四云真实创建/删除E2E与原CloudPanel完整覆盖仍有证据缺口。
- Azure删除VM后关联磁盘、公网IP等可能继续计费。

当前版本供自行部署和测试，不是生产稳定版。安装与beta.2无损升级说明见安装包README；正式附件须来自成功发布工作流，并经匿名下载、SHA-256、GHCR回拉与实际安装/升级验证后才可宣布完整交付。
