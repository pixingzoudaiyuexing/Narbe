# beta.2 四云统一体验候选交付

状态：实现与本地自动化验证完成；Docker/CI验证正在补齐。正式独立审核 REQUIRED / PENDING，beta.2尚未发布，不能宣称最终PASS。Owner现有服务器仍为beta.1，未修改其HTTP/HTTPS、服务容器或正式Volume；beta.1公开Tag/镜像不变。

## 实际行为

- 云账号自动异步统计真实云端当前访问范围内的服务器数量、运行/关机/其他状态、采集区间、查询完整性。服务器页默认全部地区，账号/地区/状态/名称/IP筛选、详情和每页50行。
- AWS：DescribeRegions获取已启用地区，3并发地区逐页DescribeInstances，限速/5xx最多3次退避；失败地区保留清单及已成功结果，可单独重试。DescribeInstanceTypes批量补CPU/内存/架构/本地存储，元数据失败不隐藏实例。
- Azure：只查询当前接入资源组VM列表，明确非整个订阅；严格校验VM资源组ID，3并发地区读取电源/网络，SKU补配置。地区失败可重试，外部资源组返回项不会计入总数。
- Linode/DO：官方账号级完整分页，地区只是客户端筛选；Linode specs与类型/地区价格、DO size配置/小时/月USD价格。公网IP过滤私有地址。查询失败或超限不冒充零台。
- 只读缓存：最多8个账号、2个扫描、90秒整体期限；15秒手动刷新节流，ready缓存从完成起60秒。局部重试独立保留首次全量完成锚点，过期自动升级全量查询，避免无限延长旧成功地区。每账号绑定credential revision，轮转/退休取消旧扫描，完成的云任务使旧缓存失效。
- 中文地区使用明确官方映射，原始ID不变；未确认/新地区保留官方名和代码。列表/创建/详情/筛选/危险确认一致。
- 创建表单显示CPU/内存/磁盘/架构和可确认价格，核对摘要含账号/地区/系统/网络/费用；切地区清空旧配置并取消/忽略旧响应。AWS EBS/Azure Managed Disk不伪装成套餐固定系统盘。未知报价不显示免费。
- HTTP弹窗使用getRandomValues构造UUID v4兼容，缺少CSPRNG明确失败；同一弹窗重试ID不变。管理操作捕获账号ID、实例原始地区/资源ID；服务端原确认和worker实时预检不消费显示缓存。
- 服务器与任务状态分开中文显示，Azure资源就绪不冒充电源运行；Azure仅关机保留计算分配明确提示仍计费。

## 已执行证据

- 两项HTTP/全地区原缺陷在修复前浏览器失败，修复后通过。
- 内部复核发现慢扫描ready结果立即失效、连续局部重试无限延长缓存两项Medium；新增确定性测试先失败后通过，并覆盖未过期局部重试继承锚点。
- 完整Go race、go vet、四云HTTP合同、真实SQLite、认证/CSRF/幂等/queued恢复与此前六项Finding回归通过。
- 浏览器：真实Go API/SQLite + Mock云的创建/响应丢失重试/删除确认；真实非secure HTTP narbe.test四云UI、全区域统计、中文搜索、状态/IP筛选、规格/价格/磁盘、镜像兼容、账号/地区异步竞争、部分失败重试、123条资源分页及缺CSPRNG失败关闭通过。没有真实云写请求。
- 节流/缓存/并发/credential revision/退休/secret错误保护、跨账号跨地区同名同ID确认令牌不得串用、旧目标消失必须拒绝，均已通过。
- govulncheck：0个可达漏洞；1个required module advisory无调用路径，不宣称依赖数据库绝对无advisory。打包3项、Docker清理CLI9项、Prettier、Actions actionlint通过。
- **真实Linode只读验证**：新版适配器在Owner授权服务器运行，查询完整、33个地区、6台实例全部运行中，6台CPU/内存/磁盘均可读取；无失败/警告、0次云写。使用服务器本地私有SQLite/key快照，探针及快照随即清除，原服务与数据未改动。不是创建/删除E2E。
- 其他三云没有Owner真实凭据，本轮只有HTTP合同和浏览器API替身，不升级成真实云验收。

## 浏览器截图

以下是实际Chromium交互截图，账号、实例、价格为测试替身数据，不是四云真实资产截图。

| 厂商 | 服务器 | 云账号统计 | 创建配置 | 核对摘要 | 手机列表 |
|---|---|---|---|---|---|
| AWS | [服务器](images/beta2/aws-servers.png) | [账号](images/beta2/aws-accounts.png) | [配置](images/beta2/aws-create-top.png) | [摘要](images/beta2/aws-create.png) | [手机](images/beta2/aws-servers-mobile.png) |
| Azure | [服务器](images/beta2/azure-servers.png) | [账号](images/beta2/azure-accounts.png) | [配置](images/beta2/azure-create-top.png) | [摘要](images/beta2/azure-create.png) | [手机](images/beta2/azure-servers-mobile.png) |
| Linode | [服务器](images/beta2/linode-servers.png) | [账号](images/beta2/linode-accounts.png) | [配置](images/beta2/linode-create-top.png) | [摘要](images/beta2/linode-create.png) | [手机](images/beta2/linode-servers-mobile.png) |
| DigitalOcean | [服务器](images/beta2/digitalocean-servers.png) | [账号](images/beta2/digitalocean-accounts.png) | [配置](images/beta2/digitalocean-create-top.png) | [摘要](images/beta2/digitalocean-create.png) | [手机](images/beta2/digitalocean-servers-mobile.png) |

## 发布门禁与待补证据

Docker构建、旧版升级、最终镜像/磁盘清理和远端CI结果在本轮结束前回填。本轮新beta.2尚无发布digest或Tag，不可冒称已发布。

发布配置、中文Notes和beta.1无损升级命令已准备在release/。只有上游正式独立审核PASS后，Engineer才设置仓库变量NARBE_BETA2_REVIEWED_SHA到实际通过的提交；工作流严格比较应用、Dockerfile、脚本、工作流与可执行安装文件（仅允许Notes/README文字后补）。变量为空时构建/发布链跳过，不代表镜像发布成功；SHA无效或不匹配时失败关闭。随后仍须两架构原生构建、GHCR拉回安装验证、匿名拉取、Tag/Pre-release及digest证据，禁止覆盖beta.1或latest。

正式Independent Reviewer需复核从v0.1.0-beta.1到本轮HEAD的全部diff，重点是聚合资源身份、缓存失效、部分失败与管理操作边界。内部只读Codex复核及agy均不替代正式审核。真实四云创建/删除E2E、原CloudPanel完整功能覆盖仍是缺口，Azure残留资源费用风险继续保留。

agy Specialist Calls: 2
- Research Scout: 0
- Root-Cause Challenger: 0
- Design Challenger: 1（已完成）
- Test Designer: 0
- Embedded Reviewer: 1（150秒超时，无结论）

Embedded Review Calls:
1 / 0 completed / 1 failed / 0 re-review

另外完成两名内部Codex只读视角的正确性/安全/兼容性复核；两项Medium修复并源码复核关闭，不是Formal Independent Review。
