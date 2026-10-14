# Narbe v0.1.0-beta.2 — 四云统一资源总览

这是公开预发布测试版本，供 Owner 自行部署和测试。beta.1 Tag及GHCR镜像保持不变，不使用 latest。

- AWS、Azure、Linode、DigitalOcean统一账号资源数量、运行/关机/其他状态、刷新时间和查询完整性。默认全部地区；部分失败明确保留已发现资源并可重试。Azure明确限定当前资源组，非整个订阅。
- 中文地区加原始ID，支持地区/状态/名称/IP筛选；新地区保留官方名称，不猜测位置。大列表每页最多50行。
- CPU、内存、套餐磁盘与独立系统盘区分展示；Linode地区价格、DO小时/月USD广告价格；AWS/Azure报价未知时明确提示，不显示免费。
- 创建表单显示配置摘要、兼容镜像与费用说明，切地区清空旧配置并忽略过期响应。公网HTTP创建/管理弹窗使用CSPRNG兼容UUID，确认及幂等安全机制不变。
- Go + SQLite + 单个非root轻量容器，Linux AMD64/ARM64，持久化数据库和master.key，日志有界。

beta.1六项独立审核修复仍保留。beta.2已完成正式独立审核，结论 PASS WITH CONDITIONS — RELEASE APPROVED；审核基线为 `e7aaadef8cf1e34a08b9b345fc3e8ba3970b6c72`。四云聚合、跨账号/地区操作边界、HTTP兼容、缓存/并发/恢复及无损升级均获通过。发布供应链按条件验证：受保护应用、脚本及可执行发布文件须与审核提交相同，仅本说明与安装README文字允许后补；发行附件记录实际发布提交、审核SHA、digest及成功工作流。

真实验证严格分级：Owner授权的现有Linode账号完成新版适配器只读地区、规格与跨地区实例查询；其他三云为HTTP合同与浏览器API替身验证。四云真实创建/删除E2E仍未完成，原CloudPanel完整覆盖仍有证据缺口。Azure删除VM后关联磁盘、公网IP等可能继续计费。

适用于自行测试，不宣称生产稳定。安装及beta.1无损升级说明见安装包README。`release-evidence.json` 与包内 `release.json` 记录准确构建SHA、审核SHA、镜像digest和原生架构验证工作流；不要把只读查询或Mock测试当作真实云写入验收。
