# Narbe v0.1.0-beta.4 — 四云初始化脚本与 Linode StackScripts

这是待发行的测试预发布候选，**尚未发布**。应用审核锚点 `2f23ceb8c1e98fcb8535e9dd5d1230e6c7e01c49` 为防火墙修复后的应用候选，应用专项复审 REQUIRED / PENDING（原初始化业务e226已PASS WITH CONDITIONS）；发行配置专项复审 REQUIRED / PENDING，不能先填发行审核SHA。审核后发布仍需上游明确授权；保留beta.1/2/3，不推latest。

新增能力：

- AWS Linux Shell、Cloud-init与Windows PowerShell。
- Azure Linux Shell / Cloud-init（osProfile.customData）。
- Linode Metadata初始化，动态核实镜像和地区能力。
- Linode现有StackScripts选择、名称搜索、分页、兼容性与动态UDF参数。
- DigitalOcean Shell / Cloud-init。
- 四云统一初始化脚本UI，UTF-8字节预算、摘要指纹和弹窗内草稿保留；普通任务与日志不披露脚本全文或参数值。

重要限制：

- 脚本主要在首次部署时执行，不等于每次重启执行；实例创建/开机成功不等于脚本执行成功。
- AWS Windows PowerShell依赖兼容的EC2Launch / EC2Config。Azure Windows customData不提供自动执行保证，本候选未开放此模式，不通过扩展绕过。
- Linode Metadata必须使用兼容系统镜像及地区；私人StackScripts权限由实际Linode账号决定，公共目录可读不能证明私人IAM。
- 四云真实收费创建、来宾执行和私人StackScript IAM尚未完成真实E2E；Linode九项高级写操作与原CloudPanel完整覆盖仍有缺口。
- 脚本可能拥有root权限，不应硬编码敏感凭据。厂商/来宾可能保留User Data，Base64不是加密。
- 保留beta.3新建Linode默认legacy_config / firewall_id=-1：不绑定云防火墙、全部放行，不更改已有实例、账户全局默认或OS防火墙。
- 保留既有重装删盘、克隆收费/源机关机、迁移停机换IP、复杂Volume/VPC/VLAN保守限制。Azure删除VM后关联磁盘及公网IP等仍可能计费。

Narbe只能说明“初始化脚本已提交，实际执行结果请登录服务器检查。” Linux日志 `/var/log/cloud-init.log`、`/var/log/cloud-init-output.log`，Azure另查 `/var/log/waagent.log`，AWS EC2Launch v2查 `C:\ProgramData\Amazon\EC2Launch\log\agent.log`，具体随代理版本变化；StackScript按脚本自身配置查看日志。

Go + SQLite、单个非root容器，无额外数据库。发行配置保留AMD64/ARM64原生构建、GHCR回拉安装与beta.3固定digest同Volume升级；实际执行结果须以审核后正式工作流为准，当前不能宣称已完成正式发布。不是生产稳定版，不自动升级Owner环境。
