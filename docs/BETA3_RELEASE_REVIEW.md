# beta.3 发行配置针对性复审材料

日期：2026-10-15。应用正式审核 PASS WITH CONDITIONS；发行配置新增差异正式复审 PENDING。此阶段只准备、提交和测试，不创建beta.3 Tag、推送正式GHCR版本或创建Release。原PR #1保持Draft，main/Owner环境不动。

## 审核范围与两个基线

应用基线固定为 `4f1c9cc2a7c8e099ab3c375548c48e691d87c72d`，已审核的九项Linode功能无需重审。本轮不修改cmd/、internal/、Go模块、Dockerfile、.dockerignore、compose.yaml、scripts/、前端依赖或运行配置。

发行基线是本轮针对性复审实际通过的最终提交（当前尚未批准，不能预填成已审核）。两项独立仓库变量：

- `NARBE_BETA3_APP_REVIEWED_SHA`：固定上述应用SHA，已获授权，可设置并回读。
- `NARBE_BETA3_RELEASE_REVIEWED_SHA`：本轮提交正式通过后才设置；准备阶段保持不存在。不会复用/修改 `NARBE_BETA2_REVIEWED_SHA`。

## 门禁实际步骤

1. push只监听原开发分支的Workflow/Dockerfile/release路径。发布前置 `source-gate` 仅contents:read；变量缺失则整条发行链跳过，不是发布成功。非空但不合法则失败关闭。
2. 先比对应用变量为固定40位正式SHA，发行变量为40位小写hex；`git show <发行审核SHA>:release/verify_source.py`提取已审验证器到runner临时目录，**不运行HEAD中的未审验证器**。
3. 验证两个SHA都直接指向commit，且应用→发行审核→发布HEAD为祖先链，checkout HEAD与发布SHA一致。
4. 应用基线→发行审核只允许以下**精确路径**变化，整棵Git树的其他差异（含新增/删除/重命名/权限变化）全部拒绝：

   `.github/workflows/release.yml`、`.github/workflows/ci.yml`、`release/VERSION`、`release/publish.sh`、`release/bundle.py`、`release/test_bundle.py`、`release/verify_source.py`、`release/test_verify_source.py`、`release/README.md`、`release/NOTES.md`、本文。

5. 发行审核→发布HEAD只允许 `release/README.md`、`release/NOTES.md` 文本内容，且必须保留100644普通文件，不能变可执行文件或符号链接。其他路径（包括本文、测试、Workflow或验证器）任何变更都拒绝。
6. 校验发行版本为beta.3，tracked工作树必须与发布HEAD一致；未知cmd/internal/scripts/release/.github或构建输入文件拒绝，防额外源文件进入构建。临时产物仅存忽略的.local目录。
7. 前置任务输出approved=true后，原生build才启动；**publish需同时等待source-gate和两架构build**，唯一packages:write位于publish job。publish在执行发行脚本前再提取已审验证器复核；`GH_TOKEN`仅注入实际发布step，publish.sh也在docker login前再验证。未审脚本差异不能通过正常发行链进入发布执行。
8. 镜像label.source revision使用实际发布HEAD；release.json分别记录`applicationReviewedSha`、`releaseReviewedSha`、`sourceSha`、版本、index及两个平台digest、workflow URL。安装包/公开证据继承同一记录。不可变标签的存在检查和部分同commit重试规则保留，不覆盖beta.1/2/3，不推latest。
9. AMD64/ARM64保留原生构建及GHCR回拉安装，并新增两架构发行镜像从beta.2 pinned digest实际升级测试。随后才生成安装包；GitHub Tag/Pre-release、公开权限、匿名拉取/附件和解包安装仍需Engineer在正式获准后逐项交付，不把工作流成功当完整发布。

权限信任边界：GitHub仓库/变量/Workflow管理者本来可改写审批值、工作流或另建有权限工作流。本方案验证受控发行链的提交来源与执行顺序，不声称阻止仓库管理员绕过其自身权限；正式发行SHA必须由上游审核后提供，不由本地测试或内部复核代替。

## 文件职责

| 文件 | 修改 |
|---|---|
| release/VERSION | 目标beta.3 |
| release/publish.sh | beta.3独立变量、token前复核、双审核SHA证据，保留不可覆盖检查 |
| .github/workflows/release.yml | 无发布权限source-gate、已审验证器、needs屏障、beta.3并发组、原生回拉后beta.2升级 |
| release/verify_source.py | 严格双SHA和精确路径/文件类型/祖先/工作树验证 |
| release/test_verify_source.py | 临时Git仓库对抗测试及Workflow权限/依赖契约测试，不接触正式仓库数据 |
| release/bundle.py、test_bundle.py | beta.3及独立应用/发行审核SHA元数据验证，digest固定安装包 |
| .github/workflows/ci.yml | 在现有只读CI增加门禁和打包单测；Action版本、权限及现有测试保留 |
| release/README.md、NOTES.md | 九功能、真实E2E缺口/费用风险、beta.2同Volume升级文案；准备阶段明确未发布 |

## 验证与证据

本地：`python3 release/test_verify_source.py`（12顶层及参数子用例）、`python3 release/test_bundle.py`（3项）、`bash -n release/publish.sh`、固定版本actionlint 1.7.7检查两个Workflow、`git diff --check`。

Git测试覆盖：合法双审核链、仅指定文案后补；应用/Docker/模块/scripts/未知Workflow/未知文件伪装发行变更拒绝；发布后篡改脚本/验证器/Workflow/版本/本文拒绝；空/短/错SHA及错误应用锚点拒绝；非祖先/checkout不符拒绝；文案变symlink/executable拒绝；脏tracked或额外未跟踪build输入拒绝。三项定向变异：移除应用范围检查、移除发行后范围检查、移除文案文件类型检查，均测试失败；恢复后通过。

正式发行配置尚未审核，因此不会把本轮HEAD写入发行审核变量。CI中正常单测、完整Go race、浏览器、Docker运行和beta.2升级可执行；实际release workflow因缺发行审核变量全部跳过，须实读job状态，不称其推送成功。应用字节与4f1c9cc基线无差异，已有候选镜像不用重复本机全量构建。

提交SHA、精确Compare链接、最终CI结果及Docker库存实读将在交付中报告。本材料本身也是发行审核范围；审核后不允许继续编辑本文并直接发布，除非重新批准新发行SHA。Notes/README则可在严格文本白名单下补充实际批准/发行信息并触发release路径push。

## 复审后同任务继续步骤

上游明确原Independent Reviewer针对本轮最终发行提交PASS后：回读精确批准SHA，设置/回读`NARBE_BETA3_RELEASE_REVIEWED_SHA`，只修改Notes/README批准文字触发push；验证双门禁、两架构构建/运行/回拉/升级、匿名拉取，再创建准确Tag和Pre-release，上传成功工作流原产三附件并匿名下载校验，解包正式包实际安装与升级。全过程不接触Owner服务器、真实云写或正式Volume，main与原Draft PR保持不动。

agy使用、远端CI与最终状态以本轮最终交付为准；内部支持不是正式发行复审。
