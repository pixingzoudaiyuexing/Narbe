# beta.4 Release Gate Only 专项复审材料

本轮只准备发行配置；原初始化业务e226已正式审核PASS WITH CONDITIONS；本次防火墙修复应用A及发行配置B专项复审均 REQUIRED / PENDING。应用审核锚点固定为 `2f23ceb8c1e98fcb8535e9dd5d1230e6c7e01c49`（仅增加本轮防火墙修复及回归，发行内容已通过恢复提交2f6d44d回到e226相同版本）。不重新开发或审核四云初始化业务。A范围及18张截图见 [应用专项复审材料](BETA4_FIREWALL_FIX.md)。历史168dde3不作为本轮批准值。

## 两个独立审核范围

准备阶段两个beta.4变量**均不设置**，已回读确认不存在。保留beta.2和beta.3原变量，不覆盖、不复用、不删除。

专项复审PASS并得到明确发行授权后，才由上游确定并设置/回读：

- `NARBE_BETA4_APP_REVIEWED_SHA=2f23ceb8c1e98fcb8535e9dd5d1230e6c7e01c49`
- `NARBE_BETA4_RELEASE_REVIEWED_SHA=<原Independent Reviewer正式批准的完整40位发行配置commit>`

第二项不能自行填写最新HEAD；当前不存在已批准发行SHA。两者必须指向Git commit本身，不能是tag/tree/blob对象、分支名或缩写。应用→发行审核→发布HEAD必须逐段满足祖先关系。

## 精确差异白名单

应用审核→发行审核仅允许11个精确路径：

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
docs/BETA4_RELEASE_REVIEW.md
```

这是可针对性复审的范围，不代表任意release目录文件都允许。Dockerfile、模块、依赖、构建脚本、cmd/internal/前端、安装器、registry工具和Compose均不允许改变；CI当前无需修改。所有白名单文件必须保持普通blob，publish.sh为100755，其他为100644，不接受symlink、目录、gitlink、删除或execute-bit变化。

发行审核→发布HEAD只允许release/README.md、release/NOTES.md普通UTF-8文字内容，必须100644、无NUL/二进制控制字符；包括本文在内任何其他文件、权限/类型差异全部拒绝。发行审核后不得继续改本文或测试再直接发布，必须重新批准相应SHA。

## 受控发行链和权限

1. 原分支push按原有Workflow/Dockerfile/release路径触发。只引用全新的BETA4变量；缺一则source-gate及整个下游链skipped，绝不误用beta.3旧变量发布。
2. source-gate只有contents:read。先硬比对应用SHA和发行SHA形状，再用 `git show "$RELEASE_REVIEWED_SHA:release/verify_source.py"` 提取审核提交的验证器到RUNNER_TEMP运行，不无条件执行HEAD验证器。
3. 验证器核实commit对象、固定应用锚点、两段祖先、精确路径、所有发行文件模式、文案UTF-8、beta.4版本、checkout SHA、tracked工作树和额外未跟踪构建输入（包括Git ignored文件，避免go:embed注入）。approved=true仅在成功后输出。
4. 原生build矩阵为ubuntu-24.04 AMD64及ubuntu-24.04-arm ARM64，各自在构建前重新提取已审验证器复核，再检查uname和实际镜像架构；都必须健康运行成功。
5. 唯一packages:write job为publish，needs同时包含source-gate和build矩阵，显式要求两者success及approved=true；先复核审核脚本，再在实际发布step注入GH_TOKEN。publish.sh在docker login前再次调用已审验证器。其他job无GHCR写权限，checkout不持久化凭据；Actions仍为原有固定完整SHA，没有新增依赖。
6. beta.4独立并发组、版本与标签断言。目标及两个架构标签存在性检查保留，不覆盖beta.1/2/3，不推latest。镜像与证据记录实际sourceSha、applicationReviewedSha、releaseReviewedSha、两个manifest digest、index digest和workflowUrl。
7. 正式发行后两架构从GHCR回拉固定digest，执行实际安装、登录、重启/重建、日志/安全检查及beta.3升级，再生成安装包。bundle验证beta.4应用锚点、版本、三SHA/digest形状、两个平台及官方workflowURL，SHA256SUMS和release-evidence继承同一真实记录。

本链的信任根是上游批准的发行SHA及GitHub仓库管理权限。能改Actions变量/任意Workflow的仓库管理员本来能另开写权限流程；本机制不声称阻止管理员改写其自身授权，也不把内部测试当作正式独立审核。

## 升级、数据与版本说明

旧镜像固定公开beta.3：

`ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.3@sha256:1448084e11d2dbdb1379628d909a29df401b999272470985e49da2e6291f842f`

原正常CI已使用该基线；本轮发行Workflow同步由beta.2改到beta.3。复用既有隔离disposable Volume测试，逐字节比较master.key哈希、密码/settings和非空加密账号凭据，保持同一Compose项目/Volume及端口/环境；强制重建后登录。绝不使用Owner Volume或执行真实云写。

README/Notes新增八项四云初始化能力、首次部署时机、创建不等于执行、AWS代理依赖、Azure Windows不开放、Linode镜像/地区和私人IAM要求、root权限与敏感数据风险、Linux/waagent/EC2Launch日志以及原Linode默认不绑定云防火墙/全部放行风险。下载命令标为发行后才可用，不能把准备稿说成已发行。

## 验证计划和交付证据

执行release门禁与打包单测、bash -n、git diff --check、actionlint 1.7.7；临时Git仓库覆盖合法双SHA及文案路径，非白名单业务/构建输入、验证器/Workflow/publish替换、非法对象/SHA/祖先链、文案symlink/可执行/二进制、未跟踪注入、缺变量及旧变量不触发。CLI提取已审验证器后，恶意HEAD验证器不执行；缺发行SHA时publish在任何registry命令前退出。核心防护定向变异必须先红后绿。

正常Push/PR CI仍执行完整race/vet、四组浏览器、Docker健康和beta.3升级。发行Workflow预期全部skipped，仅证明缺变量阻止执行，不代表双架构正式发行完成。现有ARM64候选镜像的应用/构建输入与已审应用基线一致；不作无价值本地重建，可复用镜像再次运行/升级验证。正式版本labels/digest、GHCR推送回拉、安装包公开下载等只能在后续实际发行验证。

最终提交、精确Compare、实际CI/变异结果、Docker占用与未发行状态在Engineer最终交付报告中给出；本文纳入待审配置提交，不能循环预填自己的commit。

## 已执行的本地结果

- source-gate 17顶层测试（含路径/模式/字段子用例）及bundle 4测试通过；bash -n、git diff --check、actionlint 1.7.7两Workflow通过。
- 9项定向变异均非零失败：移除应用差异检查、发行后差异检查、文件模式检查、应用祖先检查，改从HEAD提取验证器，删除缺发行变量条件，删除publish对build依赖，移除打包平台校验、忽略构建输入绕过；逐项恢复后全套通过。
- ignored构建输入测试在原exclude-standard逻辑下确实失败，修复后通过；脚本类型/权限、二进制文案、commit对象/祖先、缺变量不触registry等有实际临时Git/CLI拒绝证据。
- 本机复用相同ARM64候选ID `sha256:4cc4882287d5b3746aefc923a1bb29ffbc4d4a9f2c2b457d5e5805ee797ce5c5` 完成runtime与固定beta.3→候选Compose升级，非root/只读/日志限制、master.key/密码/非空加密账号行和强制重建登录通过。候选Docker存储18.74MB，inspect.Size 5,512,416B。不是正式beta.4带版本labels的发行镜像。
- 本轮因应用修复执行一次本机构建；专用Builder/缓存及临时候选tag由构建脚本定向清理，验证脚本只删除自己的测试容器与disposable Volume。保留新候选、beta.3及其他项目镜像，不全局prune。
- 历史168dde3准备阶段agy超时不作为新审核结论。本次应用Evidence Pack内部复核返回PASS，仍不替代A/B两组Formal Independent Review；本轮发行配置仍PENDING。

## 审核后的同任务继续

由Project + Product Manager安排原Independent Reviewer仅复审本轮发行配置。PASS和明确发行授权后，设置并回读上述双变量，只补README/Notes批准文字触发，等待完整门禁/双架构构建/推送/回拉/升级/bundle成功，再创建准确Tag/Pre-release、上传真实工作流三附件，匿名下载校验并验收安装。任何一段失败不能宣布RELEASED。本轮不执行这些写动作；PR #1保持Draft，main、旧版本及Owner部署不动。
