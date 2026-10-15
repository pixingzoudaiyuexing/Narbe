# Narbe v0.1.0-beta.4 — 四云初始化脚本与 Linode StackScripts

这是已获正式测试预发布授权的 beta.4。A 防火墙修复 **PASS**，B Release Gate **PASS**，原四云初始化脚本业务已正式审核通过，综合发行资格 **APPROVED**。应用审核SHA `ebc933f2842be343ffcb6eefd240cdab54ef1b4d`，发行审核SHA `5432c681bc66ec5d8f46914ad5cbc7dbd9fd3a04`。**本源码提交时尚未发布**；实际发行、公开镜像及附件验收结果以GitHub Pre-release和release-evidence.json为准。保留beta.1/2/3，不推latest。

新增能力：

- AWS Linux Shell、Cloud-init与Windows PowerShell。
- Azure Linux Shell / Cloud-init（osProfile.customData）。
- Linode Metadata初始化，动态核实镜像和地区能力。
- Linode现有StackScripts选择、名称搜索、分页、兼容性与动态UDF参数。
- DigitalOcean Shell / Cloud-init。
- Linode云防火墙null数据异常修复：合法空集合正常显示，畸形数据和缺失规则方向安全拦截，保留Snapshot与绑定影响范围，不改变账户默认或既有绑定。
- 四云统一初始化脚本UI，UTF-8字节预算、摘要指纹和弹窗内草稿保留；普通任务与日志不披露脚本全文或参数值。

重要限制：

- 脚本主要在首次部署时执行，不等于每次重启执行；实例创建/开机成功不等于脚本执行成功。
- AWS Windows PowerShell依赖兼容的EC2Launch / EC2Config。Azure Windows customData不提供自动执行保证，本版未开放此模式，不通过扩展绕过。
- Linode Metadata必须使用兼容系统镜像及地区；私人StackScripts权限由实际Linode账号决定，公共目录可读不能证明私人IAM。
- 四云真实收费创建、来宾执行和私人StackScript IAM尚未完成真实E2E；Linode九项高级写操作与原CloudPanel完整覆盖仍有缺口。
- 脚本可能拥有root权限，不应硬编码敏感凭据。厂商/来宾可能保留User Data，Base64不是加密。
- 保留beta.3新建Linode默认legacy_config / firewall_id=-1：不绑定云防火墙、全部放行，不更改已有实例、账户全局默认或OS防火墙。
- 保留既有重装删盘、克隆收费/源机关机、迁移停机换IP、复杂Volume/VPC/VLAN保守限制。Azure删除VM后关联磁盘及公网IP等仍可能计费。

Narbe只能说明“初始化脚本已提交，实际执行结果请登录服务器检查。” Linux日志 `/var/log/cloud-init.log`、`/var/log/cloud-init-output.log`，Azure另查 `/var/log/waagent.log`，AWS EC2Launch v2查 `C:\ProgramData\Amazon\EC2Launch\log\agent.log`，具体随代理版本变化；StackScript按脚本自身配置查看日志。

Go + SQLite、单个非root容器，无额外数据库。发行配置保留AMD64/ARM64原生构建、GHCR回拉安装与beta.3固定digest同Volume升级；实际执行结果须以正式工作流实际结果为准，当前不能宣称已完成正式发布。不是生产稳定版，不自动升级Owner环境。
