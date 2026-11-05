# beta.4 候选：四云初始化脚本与Linode StackScripts

日期：2026-10-15。High Risk。Formal Independent Review REQUIRED / PENDING；完成后交Project + Product Manager安排审核，未通过不发布beta.4、不合并main、不修改Owner部署。已发布beta.1/2/3保持。

## 覆盖矩阵

| 厂商 | Shell | Cloud-config | Windows PowerShell | StackScript | 实际实现/保护 |
|---|---|---|---|---|---|
| AWS | 是 | 是 | 是，需受支持启动代理 | 不适用 | RunInstances.UserData一次Base64，原文及PS包装≤16KiB，既有ClientToken不变 |
| Azure | Linux | Linux | 明确不支持 | 不适用 | osProfile.customData一次Base64，解码≤65535B；不改VM/磁盘/NIC/NSG/IP及账本逻辑，不用额外扩展 |
| Linode | Metadata | Metadata | 不支持 | 已有脚本选择 | 动态镜像cloud-init+地区Metadata；原文49149B，Metadata Base64≤65535；SS新鲜权限/版本/UDF核实；默认legacy_config/-1不变 |
| DigitalOcean | 是 | 是 | 不适用 | 不适用 | user_data纯文本≤64KiB，原SSH/IPv6/备份及标签不变 |

四云统一可选入口默认不使用脚本，切换模式保留当前弹窗草稿；目录重查、地区和镜像切换不清空文本，兼容性须重新确认。示例只在空框按用户按钮填入，支持多行/引号/Unicode。显示原始/提交UTF-8字节和各自上限；未知cloud-init/EC2Launch能力如实提示，不能保证执行。

StackScripts按当前账号读取，支持我的/公共/全部可访问、名称搜索、25条分页、简介、镜像约束、动态UDF必填/默认/oneOf/manyOf，参数不与Metadata组合。脚本源码不回传UI，确认展示模式、字节/参数指纹、脚本名称和源码/定义版本指纹。高级脚本执行不能升级成Narbe内Shell/SSH执行，未新增远程执行平台。

精确API、限制和官方发布物依据见 [BETA4_SOURCES.md](BETA4_SOURCES.md)。

## 数据与安全

沿用Session/Origin/CSRF、account revision、一次性确认摘要、同弹窗固定ID、AES-GCM加密意图、queued首写有效期、running/unknown恢复与不重放。StackScript在确认、首次提交、worker及适配器重新检查当前权限/身份/镜像/UDF指纹；旧确认遇源码/定义或账号权限变化拒绝，同ID已提交任务只认原载荷。

普通任务无脚本/UDF全文或值，只返回模式、UTF-8字节、SHA-256及StackScript名称/版本。创建/开机成功任务只说明“初始化脚本已提交，实际执行结果请登录服务器检查”；未知提交、未确认分别显示，不宣称脚本执行成功。云厂商原始错误不回显。浏览器不使用localStorage/sessionStorage保存脚本，关闭弹窗清理草稿与参数；模板标签/描述全部按文本转义。

本轮只confirm/jobs认证请求体上限改为1MiB，以容纳合法64KiB脚本JSON转义；各厂商脚本预算独立更严格，其他接口128KiB不变。StackScript查询复用已有2并发/30秒限制与账号revision回包保护，按页读取，不全量遍历公共脚本。

## 已执行测试与红→绿证据

- 后端四云HTTP合同：AWS Shell/cloud-config/PS单次编码+原始文本、Azure Linux customData及Windows/超限在依赖写前拒绝、DO原文、Linode Metadata/StackScript与legacy/-1精确请求。
- 字节边界：硬编码官方16KiB/65535/64KiB/49149，原文恰好、超1字节、多字节Unicode、PS包装预算、空/未知/混用/无效头、预Base64/重复编码检查。
- StackScript：当前认证请求及筛选分页、源码不回传、oneOf/manyOf/default/unknown参数、any/all私有镜像条件、源码/owner/images/UDF状态变化、403脱敏、恶意ID、跨账号私人权限不借用、12并发同ID仅1次创建、账号revision与worker校验。
- 密文及结果：脚本/参数不以明文存任务、不出现在jobs，实际脚本提交不等于执行；unknown创建仅一次写，已有认证/密钥/密码/queued/恢复与六项修复/九项Linode高级能力回归保留。
- Chromium：原真实Go API/SQLite+云替身创建丢响应重试/删除流程新增脚本，任务摘要/日志指引无原文；原beta.2/3四云及九能力回归；新四云桌面/手机脚本模式、UTF-8/hash、目录/模式保留草稿、Windows限制、Stack UDF/XSS、权限失败、清空/晚回包、跨账号和关闭不持久化。
- 定向变异先红后绿：放宽1字节AWS预算、重复Base64、改动DO原文、跳过SS版本比对、放行未知参数、把脚本混入摘要。另实际复现并修复UI脚本切换空草稿覆盖、清空选择被旧响应恢复，合法UDF与JS原型名称冲突改无原型对象。
- 完整Go race、go vet、格式/纯JS SHA-256已知与Unicode向量、9项构建清理CLI测试、actionlint通过；源码govulncheck0可达漏洞，1项required module advisory无调用路径。

内部两个只读视角复核已看到上述缺陷闭环，未形成新增必修项；这不是Formal Independent Review。agy Research Scout 1次120秒超时、Embedded Reviewer 1次120秒超时，无完成结论，未绕过权限/换模型。

应用源码提交 `3ae454971eb670ba19fb2218347dc0db18aedab0`；[Push CI](https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/38691287199)与[PR CI](https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/38691291801)均 completed / success，所有步骤执行通过，包含四云合同/race/vet、四组浏览器、源码漏洞扫描、Docker构建/运行及固定digest的beta.3持久化升级。没有把skipped当通过。

新脚本浏览器测试使用HTTP普通非secure上下文，显示SHA-256不依赖仅HTTPS可用的subtle.digest，后端独立校验不信任前端指纹。20张截图约2.6MiB，桌面1440×1080、手机390×844；它们是测试替身数据，不是实际云实例截图。

## Docker、升级与清理证据

本机原生Linux ARM64 Docker候选 `narbe/local:current`，镜像ID `sha256:b574f5b0796451d0bcf0d72d6928e1659bdedbc7f7c34946dfe6534f01d76c52`。这是开发候选（labels dev/unknown），未推GHCR、未生成正式beta.4 Tag。Docker image inspect报告5,511,420B；Docker实际存储18.74MB；运行二进制12,974,956B，SHA-256 `5af23ad1b09ad25ac0f0f77086597ae1f04ee278ee25bb8f5b51ff594c36e469`。scratch、非root 65532、无Go/Node开发运行时，CGO关闭；源码和最终二进制govulncheck均0可达漏洞，required module有1项无调用路径提示。

本机ARM64和远端AMD64均实际完成健康、首次登录、密码修改、重启和容器替换持久化；只读rootfs、cap-drop ALL、no-new-privileges、json-file 5m×3日志限制已检查。实际Compose使用beta.3公开固定digest `sha256:1448084e11d2dbdb1379628d909a29df401b999272470985e49da2e6291f842f` → 候选，隔离disposable Volume内保留同一项目/Volume、端口与配置，master.key哈希、密码/settings、非空加密账号凭据行逐字节一致；再次强制重建后登录正常。仅使用假云账号fixture，无真实云写，没有触碰Owner数据库/服务器。

已回收本次唯一专用Builder和缓存、候选临时tag、验证/升级容器及Volume、约510MB BuildKit工具镜像、提取二进制和约11MB研究下载。保留最终待审候选及beta.1/2/3回滚镜像；其他项目Rust镜像不动。最终Docker 5镜像合计2.324GB（其中其他项目Rust约2.211GB），0容器、0 Volume、0构建缓存，只有原有default/desktop-linux builder。主机可用14GiB（检查时点）。没有global prune、没有删正式数据或其他项目资源。

[结构化证据与日志摘要哈希](BETA4_EVIDENCE.json)记录应用SHA、两条成功CI、官方API只读探测、定向红→绿及Docker结果。最后的文档/截图提交与已测试应用SHA分开；后续docs-only提交不重建镜像，正式审核范围仍是beta.3发布基线至最终PR HEAD。

## 真实API与E2E限制

实际匿名GET官方公共StackScripts列表（is_public=true，label包含Docker）成功，page1/4、87结果、本页25，带UDF定义；未使用真实账号Token，不能证明私人脚本或账号写权限。此证据仅公开目录只读连通/schema，不是创建或来宾执行E2E。本轮全部创建/脚本运行均合同/Mock，真实收费四云创建与Linode高级操作没有授权、没有执行。

云厂商/来宾具体agent版本、脚本语法、软件源/外部依赖、私有镜像及真实IAM、真实执行日志仍需Owner后续自行验证。Azure Windows customData不自动执行；其他无法确认代理时只做兼容性确认不保证。原CloudPanel完整功能覆盖与四云真实写E2E缺口继续保留。

## 浏览器截图（真实Chromium，测试替身数据）

| 功能 | 桌面 | 手机 |
|---|---|---|
| AWS Shell | [查看](images/beta4/beta4-aws-shell-desktop.png) | [查看](images/beta4/beta4-aws-shell-mobile.png) |
| AWS Cloud-config | [查看](images/beta4/beta4-aws-cloud-config-desktop.png) | [查看](images/beta4/beta4-aws-cloud-config-mobile.png) |
| AWS PowerShell | [查看](images/beta4/beta4-aws-powershell-desktop.png) | [查看](images/beta4/beta4-aws-powershell-mobile.png) |
| Azure Linux | [Shell](images/beta4/beta4-azure-shell-desktop.png) · [Cloud-config](images/beta4/beta4-azure-cloud-config-desktop.png) | [Shell](images/beta4/beta4-azure-shell-mobile.png) · [Cloud-config](images/beta4/beta4-azure-cloud-config-mobile.png) |
| Linode Metadata | [Shell](images/beta4/beta4-linode-shell-desktop.png) · [Cloud-config](images/beta4/beta4-linode-cloud-config-desktop.png) | [Shell](images/beta4/beta4-linode-shell-mobile.png) · [Cloud-config](images/beta4/beta4-linode-cloud-config-mobile.png) |
| Linode StackScript | [查看](images/beta4/beta4-linode-stackscript-desktop.png) | [查看](images/beta4/beta4-linode-stackscript-mobile.png) |
| DO | [Shell](images/beta4/beta4-digitalocean-shell-desktop.png) · [Cloud-config](images/beta4/beta4-digitalocean-cloud-config-desktop.png) | [Shell](images/beta4/beta4-digitalocean-shell-mobile.png) · [Cloud-config](images/beta4/beta4-digitalocean-cloud-config-mobile.png) |

agy Specialist Calls: 2
- Research Scout: 1（超时）
- Root-Cause Challenger: 0
- Design Challenger: 0
- Test Designer: 0
- Embedded Reviewer: 1（超时）

Embedded Review Calls:
1 / 0 completed / 1 failed / 0 re-review
