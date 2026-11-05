# beta.5 Owner 直接授权发行记录

2026-11-05。Owner 明确取消本轮额外 Independent Reviewer 人工复审，授权整合 PR #2、完成必要自动化验证并直接发布 v0.1.0-beta.5 测试 Pre-release。此授权不等于新增 Independent Reviewer PASS，发行证据不得混淆。

## 来源与权限

- 原发行候选 `e95e0b7958e31a7372ff4384e5bf5b9a7652f5a8`。
- PR #2 HEAD `ec66fd4e90f38a1940b8cdf662868bd1a5a0a70e`，已自动化验证应用 `3216f1e9e5151338427c7ceee5fe0090fdebf1e4`。
- 保留历史的整合 Merge `8956a57830f0a853ccf69dc7326eb22149d07193`，应用、测试、Dockerfile 与 PR #2 相同。
- 本次新增发行配置、文案及授权记录后形成唯一固定源码 SHA，使用 `NARBE_BETA5_OWNER_AUTHORIZED_SHA`；不设置旧 `*_REVIEWED_SHA` 冒充审核，不改历史 beta.2/3/4 审核变量。
- `release/OWNER_AUTHORIZATION.json` 记录 Owner 授权日期、原话、免人工审核和范围；最终 SHA、真实 CI URL、镜像 Index／平台 digest 写入成功工作流的 release-evidence.json。

原双 SHA 历史验证代码和测试保留，但当前 Workflow 只执行明确的 Owner 路径。此路径要求完整40位直接Commit、已验证应用→PR2→授权源码的祖先链、授权源码严格等于发布HEAD（即便后续普通文案也不能冒用原批准），PR2之后只允许精确列举的发行文件变化且保持普通文件类型/权限。应用、Dockerfile、Go依赖、前端或其他构建输入变化失败关闭。包括ignored文件在内的未跟踪构建输入、脏工作区亦拒绝。

source-gate及两个原生build和publish都提取**授权SHA内**的验证器；没有使用HEAD外部可变代码。仅publish有packages:write，显式依赖source-gate、verify-ci、两平台build全部success；所有Actions固定完整SHA，checkout不持久保留凭据。verify-ci仅actions:read/contents:read，限定官方repo、ci.yml、push分支和精确源码SHA，最新对应run、全部job和step均需success，不能接受skipped/failed/旧源码。

发布后在两原生架构用空DOCKER_CONFIG **匿名实际pull** Index Digest，Compose安装／登录／持久化，执行固定beta.4→最终公开镜像的无损升级。只有回拉双平台通过才生成安装包；脚本和Workflow不创建Git Tag或GitHub Release，待整个发行Workflow成功、附件核对后由Engineer按本次Owner授权创建准确Tag与Pre-release并公开下载校验。

## 功能与限制

本次beta.5同时包含四云API代理/三出口检测/只读诊断、Linode重装初始化、无账号阻断与四Worker、自动关机改密恢复、单次确认、云新密码默认可见与unknown不重放，以及Go1.26.10修复。单容器Go+SQLite、原master.key及密文保护保持。

真实云写和Linode自动密码重置／来宾登录E2E未完成；所有自动写回归使用隔离替身。Owner服务器、真实云资源、main不修改。beta.4不认识beta.5代理配置，保留旧镜像并不证明新数据无损回滚，须保留升级前完整数据库+key+部署配置备份。

执行中的验证与发行结果以实际GitHub Actions、不可变digest和正式Release附件为准。尚未完成时不能宣称RELEASED。最终证据在release-evidence.json及Engineer交付中列明，本文件不循环预填自己的发布提交SHA。
