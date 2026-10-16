# beta.5 官方依据与能力边界

核对日期：2026-10-16。本轮只新增云 API 出口和 Linode Rebuild 初始化；其他三云重装仅研究，未增加任何云写流程。

## Linode Rebuild

[官方 Rebuild API](https://techdocs.akamai.com/linode-api/reference/post-rebuild-linode-instance)；[官方 OpenAPI](https://github.com/linode/linode-api-openapi/blob/main/openapi.json)。本次取到 OpenAPI 4.229.1，下载文件 SHA-256 `bb8daa30ee983e393e343cf05cf078fa7b27a9f9a5b12fd97a901116a8dfc792`。

- 重装关机并删除所有磁盘与配置文件。网络接口不改变。本实现不提交 firewall、interfaces、type 或 region，不借此重绑防火墙或调整实例身份。
- `metadata.user_data` 为一次 Base64 后最多 65535 字节，因此沿用 beta.4 原文最多 49149 UTF-8 字节。Shell / Cloud-init 仍须镜像 cloud-init 与地区 Metadata 能力。
- 目标 `has_user_data=true` 时官方要求重新提供 Metadata 来继续使用。省略字段不具有保留旧 User Data 的保证；界面展示当前状态、显著告知并要求单独确认，含“不使用脚本”模式。
- `stackscript_id` / `stackscript_data` 与 Metadata 互斥。复用创建时的实时权限、源码/UDF 指纹、镜像、必填和 oneOf/manyOf 校验；Rebuild 另拒绝私有镜像加 StackScript。官方该端点明确禁止该组合，不能套用创建端点的 any/all 私有镜像条件。
- 官方 StackScript 参数上限是 65535 字符；本实现沿用更保守的 UTF-8 JSON 字节预算。API 受理及目标开机都不证明脚本实际执行成功。

## 云 API 代理与 IP 服务

[Go net/http Transport](https://pkg.go.dev/net/http#Transport)、[net/url](https://pkg.go.dev/net/url)；核对本项目 Go 1.26.9 实现，HTTP/HTTPS CONNECT、SOCKS5 和 SOCKS5H 均由同一显式 Transport 工厂创建。SOCKS5 与 SOCKS5H 实际均把目标域名交给代理解析；测试读取 SOCKS 握手 ATYP=domain，未按协议名字假定本地 DNS。

[ipify 官方接口](https://www.ipify.org/) 的固定 `https://api64.ipify.org?format=json` 支持 IPv4/IPv6。浏览器链独立、credentials omit、no-referrer；后端直连 Transport 明确不读取环境代理；代理检测与云 API 使用同一 Transport 配置及安全策略。三项成功只证明该测试请求出口，不保证不同云目标共享 NAT。

## 其余三云：仅后续可行性

| 厂商 | 已核实官方能力 | 本轮结论 |
|---|---|---|
| AWS | [CreateReplaceRootVolumeTask](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateReplaceRootVolumeTask.html) 支持替换运行中实例的 EBS 根卷，未列 UserData 参数；[User Data](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/user-data.html) 默认只在首次启动执行，修改停止实例的数据不等于重新执行 | 需独立设计根卷、来宾代理、身份、幂等与费用恢复，不把它当作 Linode 式重装脚本接口 |
| Azure | [Reimage 2024-07-01](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/reimage?view=rest-compute-2024-07-01) 有特定磁盘及 osProfile 参数；其 customData 说明禁止 VM 创建后更新，不能保证重装时替换脚本 | 需区分磁盘/VM 类型与来宾初始化支持，保留现有范围 |
| DigitalOcean | [Droplet Actions](https://docs.digitalocean.com/products/droplets/reference/api/droplet-actions/) 提供 rebuild 的 type/image，当前所核对请求字段未给出新 user_data；创建接口的 user_data 不能推断为 rebuild 支持 | 未实现重装初始化，后续须以官方明确合同及真实只读证据再定范围 |

以上是接口能力核对，不是三云重装或来宾执行 E2E。
