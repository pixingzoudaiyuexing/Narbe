当前 **beta.5 云 API 出口及 Linode 重装脚本**的 Formal Independent Review REQUIRED / PENDING。已发布 beta.4 的正式 PASS 不覆盖本轮。候选不发布、不合并 main、不变更 Owner 部署。审核范围、攻击面、实测／替身分级与缺口见 [BETA5_DELIVERY.md](BETA5_DELIVERY.md)，官方依据见 [BETA5_SOURCES.md](BETA5_SOURCES.md)。后续发行配置仍须单独准备及审核，beta.4 的发行双 SHA 不授权发布本轮业务代码。

以下为历史记录，PENDING/PASS 仅对各自当时基线生效。

当前beta.4（四云创建初始化脚本及Linode StackScripts）正式独立审核 REQUIRED / PENDING。beta.3应用与发行配置均已通过正式审核并发布，其PASS不覆盖本轮新增代码。当前交付矩阵/截图、真实与Mock分级见 [BETA4_DELIVERY.md](BETA4_DELIVERY.md)，官方接口与安全限制见 [BETA4_SOURCES.md](BETA4_SOURCES.md)。本轮不发布beta.4、不改Owner部署、main或旧Tag/镜像。

以下为历史审核记录，各PENDING/PASS只对各自基线生效。

当前 beta.3（Linode高级管理与四云大洲分组）的正式独立审核 REQUIRED / PENDING。beta.2已正式审核并发布，其PASS不覆盖本轮新增功能。九项能力、内部复核、测试和真实云缺口见 [BETA3_DELIVERY.md](BETA3_DELIVERY.md)。beta.3未发布；本轮不修改release可执行文件或发布beta.2变量，不能复用旧审核SHA发布新代码。

以下为历史记录，历史PENDING/PASS各自只对应原基线。

当前 beta.2 四云UX改进的正式独立审核 REQUIRED / PENDING；之前beta.1的PASS只覆盖其已审核提交，不能沿用为本轮PASS。发布工作流在 `NARBE_BETA2_REVIEWED_SHA` 未经上游正式审核后设置前跳过构建/发布；非发布成功。

Owner 本轮 BETA RELEASE DELIVERY 已确认应用提交 `5ffbf3479ad1e079b4b35efcaae837ecf08f2b3b` 正式复审PASS，六项Finding全部关闭。以下内容保留历史内部审查过程；不能把历史PENDING当作当前应用审核状态。发布仅允许打包/版本/工作流/文档，应用与安全运行配置保持已审核基线。

# 内部审查与正式审核状态

日期：2026-10-14。High Risk。**审核基线 c72b29a 已获正式独立审核 PASS WITH CONDITIONS；本轮六项修复仍需原 Independent Reviewer 正式复审，状态 PENDING。** 本文保留第一阶段历史内部审查记录；本轮修复详见 [正式审核修复报告](FORMAL_REVIEW_FIXES.md)。任何内部复核不授权合并或上线。

## 范围与攻击面

| 边界 | 关键约束 |
|---|---|
| 匿名浏览器 → 登录/API | 登录限速与哈希并发上限；公共 health 不含业务信息；其余 API 服务器会话校验 |
| 已登录浏览器 → 账号/云操作 | 精确 Origin、CSRF、严格 JSON、一次性确认摘要、固定请求 ID、凭据 revision |
| SQLite → 云 API | 凭据/敏感任务载荷 AES-GCM+AAD；先持久 running 后派发；固定官方 endpoint；不重放未知结果 |
| 云响应 → 浏览器 | 有界读取、结构化原因分类、文本转义；不返回原始错误体或凭据 |
| 构建脚本 → Docker socket | 唯一专用 Builder、一次运行身份、精确镜像账本、引用及标签保护、清理失败阻止新增构建 |
| PR → CI | Action SHA 固定、contents:read、无云密钥、无 pull_request_target、依赖 lock、忽略 install scripts |

## 已修正的实质问题

| 问题 | 内部状态 | 复核证据 |
|---|---|---|
| 丢失提交响应后前端更换请求 ID，导致重复创建 | 修复：同一对话框固定意图 ID | 真实浏览器注入“服务端提交后丢响应”，重试同 ID；API 幂等/并发测试 |
| 确认未绑定凭据；确认后替换账号可向另一云账户执行 | 修复：revision 绑定、事务消费时及执行前核对 | TestConfirmationCannotSwitchCloudCredentials |
| PUT 联网验证与 DELETE 并发，重新写回 retired 凭据 | 修复：事务内 active/revision 再核实，带条件 UPDATE 与影响行数 | TestCredentialUpdateCannotResurrectRetiredAccount |
| 丢失 master.key 时生成错误替代 key；错误 key 无启动校验 | 修复：缺失拒绝；GCM sentinel 匹配后恢复任务 | MissingKey / WrongMasterKey / Tamper / ProcessLock / Reopen tests |
| Linode 公共目录不能证明认证；EOL 日期无时区；SSH-only 空密码 | 修复：认证后实例读取、兼容真实日期、省略空密码 | 实际公共只读端点证据 + HTTP/API 回归 |
| AWS 手写签名、镜像分页；Azure 长名称唯一性、DiskRP 版本、NSG、LRO；DO 缺 action ID | 修复：官方 signer、分页及精确合同、稳定唯一 ID、独立 DiskRP、受限 NSG、LRO 类型/Retry-After、未知结果保护 | 四云合同测试；真实云仍未验证 |
| 清理旧镜像时机、失败候选、部分创建、库存查询失败、隐藏镜像、回滚保护 | 修复：确切 ID 账本、Compose 后再次收集、所有失败有界、查询失败 fail closed、-a 库存、pending 阻断 | 9 类 CLI 替身 + 实际一键安装/库存核对 |

三个只读协作子代理分别调查旧版、AWS/Azure、Linode/DO，并对跨层和部署做内部审查；Engineer 独立核对、修改和执行测试。最终部署脚本限定范围复核未发现新的必修问题。没有创建正式 Reviewer 长期角色，没有伪造正式审核。

## agy 实际使用

```text
agy Specialist Calls: 4 total
- Research Scout: 0
- Root-Cause Challenger: 0
- Design Challenger: 2
- Test Designer: 0
- Embedded Reviewer: 2

Completed Specialist Calls: 1
Failed / Aborted Specialist Calls: 3
Embedded Review Calls: 2 total / 0 completed / 2 failed / 0 re-review
```

1. Design Challenger — gemini-3.1-pro-high / 请求 effort=high / 180s：CLI 拒绝该模型的 effort 参数；诊断显示当前目录也未提供该 Pro 模型。按真实启动计 1 次失败，不算代码 finding。
2. Design Challenger — gemini-3.8-flash-high / 模型内置 high / 180s：completed，返回 FINDINGS。UNKNOWN 解除阻塞、Azure 计划账本和 fsync 等建议已对代码核实并落实；“锁只保护 key”“确认必须在派发时过期”等未受代码/任务合同支持的说法未当事实采用。
3. Embedded Reviewer — gemini-3.8-flash-high / 内置 high / 180s，inline 源码及测试证据：print timeout，未产生审核结论。
4. Embedded Reviewer — 同模型 / 180s，缩小到 immutable submit、worker、清理脚本：仍 print timeout，未产生审核结论。

全部通过 `/Users/wang/bin/agy --agent engineer-specialist`，没有绕过权限、调用 underlying binary 或更改代理。CLI 当前对编码了 high 的模型拒绝独立 --effort；在已固定目录内使用 flash-high，没有猜测新模型。agy 两次 Embedded Review **没有完成**；内部代码审查由实际完成的只读协作审查和 Engineer 验证支撑，不能把 timeout 报告为 PASS。

## 未验证与剩余审核门禁

* 旧版镜像实际 UI/API/设置/其他操作完整基线仍未取得；仅历史仓库提到的换 IP 与定时资产同步，不能声称本版已覆盖。
* 没有四云真实凭据，没有执行真实计费创建或删除。需审核 IAM、真实账户范围、地区容量及 provider 变体。
* Azure 删除保留数据和网络，存在持续收费风险，界面与任务路径明确提示；人工解除未知任务不证明云操作成功或失败。
* 当前为单用户、商业 AWS 分区，没有 MFA/在线主密钥轮换；生命周期数据是发行版维护窗口，不证明具体镜像补丁已更新。
* Go 依赖模块含未导入的 OpenPGP 弃用告警；源码与保留符号的实际交付二进制扫描没有可达漏洞，不能宣称整个依赖模块没有任何 advisory。

风险与任务 High 声明一致，无 Review Requirement 降级。交付状态为候选实现与本地验证完成、旧版完整覆盖未确认、真实云 E2E 未验证、正式独立审核待完成。
