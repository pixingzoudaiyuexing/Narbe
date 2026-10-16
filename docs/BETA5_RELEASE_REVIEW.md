# beta.5 Release Gate Only 正式复审材料

状态：**应用 PASS WITH CONDITIONS；发行配置 REQUIRED / PENDING，未授权发布。** 本轮仅发行准备，不重复审核已通过的 API Egress、Linode Rebuild Startup 业务。未设置 beta.5 变量，不创建 Tag、不推正式 GHCR、不创建 GitHub Release，不合并 Draft PR #1、不改 main 或 Owner 部署。

## 固定身份与差异分段

- 应用审核：`182e57759494c6fbcf6e3291d8259b524a9ea49a`，不可改为其他 HEAD。
- 已审交付证据：`800c5443cb5fb913e30fe71467642c6fdfa87b00`。应用→证据只含 `docs/BETA5_DELIVERY.md`、`docs/BETA5_SOURCES.md`、`docs/BETA5_EVIDENCE.json`。
- 本轮发行配置：证据 HEAD 之后的新增提交，最终精确 SHA 由 Engineer 报告供 Reviewer 选择，不能循环预填自己的提交。
- beta.4 正式源码 `049fda6d77adcb5a4cda72ea1284662896b6d6ff` 保持；旧审核变量、Tag、镜像和附件不变。

三份既有证据文档保持其历史内容，包括当时的 PENDING；本轮应用审核 PASS 的新事实由本文件与发行说明记录，不修改已审业务或文档快照。

## 双变量与来源链

仍只有两个独立 Actions Variables，本阶段都不存在／不设置：

- `NARBE_BETA5_APP_REVIEWED_SHA`：后续只能是 `182e57759494c6fbcf6e3291d8259b524a9ea49a`。
- `NARBE_BETA5_RELEASE_REVIEWED_SHA`：后续只能是本轮原 Independent Reviewer 明确批准的发行提交 SHA，准备阶段未知。

验证器另外固定已审证据常量 `800c544…`，不是新的可配置审核变量。校验完整 lowercase 40 位直接 commit（拒绝分支、缩写、tree/blob、annotated-tag 对象与无效对象），链为：

`应用182e577 → 已审证据800c544 → 发行审核SHA → 发布HEAD`

应用→证据仅准三份指定文档，三者必须为 100644 普通 blob、UTF-8 无控制符文本，JSON 额外解析。证据→发行审核只准下面精确路径；因此三份已审证据不能在发行配置阶段任意重写。发行审核→发布 HEAD 仅准 `release/README.md` 和 `release/NOTES.md` 的普通 UTF-8 文案，两者不可是 symlink、目录、submodule、可执行文件或二进制文本。

验证器拒绝 tracked 工作区差异以及受保护目录／构建根文件中的未跟踪输入，**包括 ignored 文件**，避免 `go:embed` 或构建注入。`node_modules`、`.local` 不属于编译输入。当前开发树已有的未知 `internal/.DS_Store` 保留，不能为门禁静默清除；真实链验证在不携带这些文件的 disposable 干净 clone 内进行。

## 精确发行路径清单

保留 beta.4 的发行路径边界，仅将审核材料文件换为 beta.5：

```
.github/workflows/release.yml
.github/workflows/ci.yml
release/VERSION
release/publish.sh
release/verify_source.py
release/test_verify_source.py
release/bundle.py
release/test_bundle.py
release/README.md
release/NOTES.md
docs/BETA5_RELEASE_REVIEW.md
```

只有 `release/publish.sh` 是 100755，其余一律 100644 普通 blob。`.github/workflows/ci.yml` 作为原有显式发行依赖路径保留，但本轮不改动它，已有 CI 自动运行新版门禁／bundle 测试。未新增任意 `docs/**`、shell／Workflow 通配或 Dockerfile／Go 依赖例外。`release/install.sh`、`release/registry.py`、`release/compose.yaml`、`.env.example`、应用、前端、构建输入均不改动，若变化则拒绝。

本轮实际只改 10 个发行文件（上述清单除 ci.yml）。相对应用 SHA 的 Diff 必须分成已审三文档与本轮这 10 文件，不能把新发行代码藏进已审应用锚点。

## Workflow 权限及顺序

仅监听原分支指定发行路径 push；不增加 dispatch／pull_request 发行入口。版本、并发组、变量、镜像标签、安装包统一 beta.5。旧 beta.4 变量不能触发 beta.5；缺任一新变量时 source-gate 预期跳过，下游不构建／不获取发行 token／不推送，不是发行成功。

source-gate 只持 contents:read，全历史 checkout 且不保留凭据，提取 `发行审核SHA:release/verify_source.py` 到 RUNNER_TEMP 执行，**不执行 HEAD 的验证器**。每个原生 build 和 publish 暴露 registry token 前重复同一提取与校验。唯一 packages:write Job 仍是 publish，显式依赖 source-gate + 两架构 build 聚合 success 和 approved=true；所有 Actions 固定完整 SHA，不增加服务或长效密钥。

原生 AMD64 / ARM64 验证主机 uname、构建、健康运行后上传同 run 架构档案。publish 校验版本、架构及 source revision；最终标签存在则拒绝覆盖，仅同源码部分重试可复用原架构标签，不推 latest。GHCR 写前后仍检查不可变标签。

verify-published 在两原生架构回拉最终 digest，实际 Compose 安装、健康、登录与 beta.4 无损升级；bundle 仅在两个回拉验证成功后生成安装包。脚本与 Workflow 均不创建 Git Tag 或 GitHub Release，也无 contents:write；后续只有全部正式发行验收成功才能另获授权创建 Pre-release。中途可能保留同提交架构镜像，但不能把它描述成完整发行。

## 证据结构与打包

安装包 `narbe-v0.1.0-beta.5-install.tar.gz`、SHA256SUMS、release-evidence.json。包内仍只有 Compose、安装器、环境示例、README、VERSION、digest 锁文件与 release.json，不携带密码、数据库、key、Go/Node 或开发依赖。

证据保留 applicationReviewedSha、releaseReviewedSha、sourceSha、version、Index／平台 Manifest digest、Actions URL，新增 `deliveryEvidenceSha` 和完整 `sourceGate` 分类记录。bundle 同步核实固定应用／证据 SHA、元数据一致、双平台及正确版本，错误记录在写输出目录前拒绝。模拟 bundle 只在临时目录中测试，不是正式安装包。

## 升级与回滚

固定升级基线：

`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.4@sha256:1d1f45b4a357fd609e818606e549b0ebabb98e8486fd4e4c43f09e542c548560`

沿用单容器、原非 root 安全配置，在隔离 disposable Volume 验证同项目／Volume、SQLite、master.key、管理员密码、加密账号、端口及 Origin；重启与强制重建后登录。

**beta.4 不识别 beta.5 代理配置，保留旧镜像不是升级后数据无损回滚证明。** 回滚须考虑旧出口行为，依赖升级前完整备份及原部署配置，在隔离 Volume 核实后才决定切换；本轮没有宣称反向数据兼容，也不自动操作 Owner 安装。禁止 down -v、替换唯一 master.key 或全局 prune。

## 需要 Reviewer 复审的内容

仅审核本轮发行 Diff、证据锚点与双变量来源链、文件路径／模式保护、ignored 输入拒绝、权限依赖、平台档案与镜像标签校验、证据结构／安装包、beta.4 升级与发行后文案范围。业务源与已审 `182e577` 相同，不重新审九项高级功能或 API Egress／Rebuild 实现。

实际测试数、失败关闭变异、最终 Push/PR CI、原生架构及升级、磁盘清理和未发布状态，由最终 Engineer 报告给出。准备阶段的发行工作流预期 skipped，不冒称正式构建／推送／回拉成功；正式 GHCR、Pre-release、公开安装与附件校验只能在复审 PASS 并获发布授权后执行。

## 本地实际测试

- 新门禁 23 项顶层测试（含文件路径／类型／权限等子用例）与 bundle 4 项通过；bash 语法、actionlint 1.7.7 两 Workflow、git diff --check 通过。
- 先用旧门禁隔离应用锚点后实际复现三份已审文档被拒绝，原错误列出全部三路径；新版正常链及发行后两文案路径通过。
- 11 项定向变异逐项红→绿：证据路径例外、业务差异检查、发行后差异检查、证据模式、发行文案模式、证据祖先边、ignored 输入、从 HEAD 提取验证器、缺发行变量条件、publish 构建依赖、bundle 双架构。恢复后完整 23+4 项重新通过。
- cmd/internal/scripts、Go 依赖、Dockerfile、.dockerignore、Compose、普通 CI 与正式应用 SHA 无差异。未在开发树清理未知文件来冒充干净状态。
