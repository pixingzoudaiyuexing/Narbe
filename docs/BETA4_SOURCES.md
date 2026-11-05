# beta.4 初始化脚本：官方API与限制依据

核实日期：2026-10-15。本轮只针对创建新实例，不在Narbe容器执行代码、不通过SSH执行，不增加已有实例脚本更新或编排平台。

| 厂商 | 官方字段/编码 | 服务端原始UTF-8限制 | 模式与兼容性 |
|---|---|---|---|
| AWS | RunInstances.UserData，适配器一次Base64 | 16,384 bytes；PowerShell额外XML包装计入 | Linux shebang Shell、#cloud-config；Windows用原始PowerShell命令，服务端包装小写powershell标签，不加persist。需要EC2Launch/EC2Config兼容代理，用户明确确认镜像兼容性。 |
| Azure | properties.osProfile.customData，一次Base64 | 解码后65,535 bytes | Linux Shell/cloud-config；Linux Agent默认不执行customData、cloud-init才会处理，镜像代理未由API证明时明确警告并确认。Windows拒绝此自动执行入口，不用扩展绕过；不混用VM userData。 |
| DigitalOcean | Droplet.user_data，纯文本 | 65,536 bytes | Linux Shell/cloud-config；镜像需要cloud-init，无可靠能力字段时显示未核实并确认，不把未知当支持。 |
| Linode Metadata | metadata.user_data，一次Base64 | 编码后最多65,535 bytes，因此原文最多49,149 bytes（编码65,532；再加1原字节将编码为65,536） | 镜像API capabilities含cloud-init、地区含Metadata才启用；不支持则拒绝，不让兼容性勾选绕过。 |

来源：[AWS User Data](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/user-data.html)、[Azure custom data](https://learn.microsoft.com/en-us/azure/virtual-machines/custom-data)、[Azure OSProfile精确65535字节](https://learn.microsoft.com/en-us/javascript/api/@azure/arm-compute/osprofile?view=azure-node-latest)、[DO User Data](https://docs.digitalocean.com/products/droplets/how-to/provide-user-data/)、[DO Create Droplet字段](https://docs.digitalocean.com/reference/api/digitalocean/#tag/Droplets/operation/droplets_create)、[Linode创建](https://techdocs.akamai.com/linode-api/reference/post-linode-instance)、[Linode原始格式与编码](https://techdocs.akamai.com/cloud-computing/docs/add-user-data-when-deploying-a-compute-instance)、[Linode官方OpenAPI](https://github.com/linode/linode-api-openapi/blob/main/openapi.json)。

Linode当前官方OpenAPI 4.229.1 的metadata.user_data说明为 “Unencoded data can't exceed 65535 bytes when encoded.” 本实现按编码后上限保守精确计算，不冒称原文也是64KiB。同一创建schema的stackscript_data描述限制JSON共65,535字符；Narbe本候选采用更保守的JSON 65,535 UTF-8字节、100字段、单值32KiB的有界本地策略，不伪装为厂商强制字段数。

Base64只是传输编码，不是加密。Narbe不接收“已经Base64编码”的Linux文本；Shell必须首行shebang，cloud-config须以该标识独占首行。PowerShell由服务端统一包装，拒绝用户自带包装、Linux头或持久化标签，避免重复格式。不会在Narbe内解析执行Shell、PowerShell或YAML；具体语法/来宾环境仍需用户验证。

## Linode StackScripts

[官方创建工作流](https://techdocs.akamai.com/linode-api/reference/create-a-linode-using-a-stackscript)、[List](https://techdocs.akamai.com/linode-api/reference/get-stack-scripts)、[Get](https://techdocs.akamai.com/linode-api/reference/get-stack-script)、[UDF规范](https://techdocs.akamai.com/cloud-computing/docs/write-a-custom-script-for-use-with-stackscripts)、[可用范围](https://techdocs.akamai.com/cloud-computing/docs/stackscripts)。

- GET /linode/stackscripts 使用当前账号认证；mine/is_public官方筛选，label +contains名称搜索，25条分页，最多请求页100。UI分别显示我的、公共、当前可访问来源，不把某账号私人脚本变成全局缓存。
- GET /linode/stackscripts/{id}重新读取当前身份权限、完整源码/镜像/UDF。源码只在适配器内计算SHA-256指纹，返回安全元数据，不返回源码。指纹包含脚本内容、UDF、images、owner/updated/mine/public等执行相关身份。
- images必须包含所选镜像或官方特殊值any/all。官方schema明确any/all可支持私有镜像，所以不一概声称私人镜像永远不能用；仍需当前账号对镜像和脚本的权限。distributed地区不支持StackScripts。
- UDF name/label/example/default/oneOf/manyOf保留；default缺失（null）为必填，显式空字符串default为可选；单选只能官方集合，多选为逗号分隔集合、拒绝重复/越界；未知参数拒绝。参数名按环境变量安全形状校验，无原型JS对象支持__proto__等合法名字，不发生属性继承冲突。
- 创建提交stackscript_id+stackscript_data；Cloud-init与StackScript互斥。不额外开发CRUD。读取失败明确提示stackscripts:read_only；真实资源IAM仍由官方API决定，不能用公共目录成功证明私人权限。
- 确认、首次入队、worker及Linode.Create都重新核实脚本指纹/参数/镜像；旧确认遇变化拒绝。同一请求ID保持原加密意图，网络丢响应不会产生新收费实例重试。资源创建前沿用原queued有效期与首个云写检查；unknown不重POST。
- 脚本可能导入其他StackScripts或下载外部依赖；本指纹固定直接所选脚本和定义，不能证明其远端依赖未来不变。用户应自行审查可信来源，不把公共脚本当安全认证。

默认新建Linode继续显式interface_generation=legacy_config、firewall_id=-1；已有/自定义FW选择保持，绝不修改账号全局默认。初始化不扩展beta.3重装/克隆等高级操作的脚本参数。

## 结果与检查

资源创建/开机成功不等于初始化成功。任务区分not-confirmed、submission-unknown、submitted-unverified，仅最后一种说明已提交，三者都不宣称来宾执行成功。日志路径取决于代理及镜像：Linux /var/log/cloud-init.log、/var/log/cloud-init-output.log；Azure Linux Agent /var/log/waagent.log；AWS EC2Launch v2 C:\ProgramData\Amazon\EC2Launch\log\agent.log。StackScript日志需按脚本自身配置查看。

任务意图继续AES-GCM加密持久化；普通任务仅模式、字节数、内容/参数SHA-256及StackScript名称/版本指纹，无全文/参数值。用户勿硬编码Token、私钥、云密码，云厂商/来宾可能留存User Data。新增请求体预算只对已认证confirm/jobs放宽至1MiB，以容纳64KiB文本的JSON转义；其他接口仍128KiB，不放宽脚本厂商上限。
