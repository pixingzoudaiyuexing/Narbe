# HTTP 创建弹窗与跨地区实例显示修复

本轮是 Owner 在已部署测试服务器上反馈的两项 UI 修复，不修改云厂商写操作、认证、任务确认、执行器或数据库结构；不覆盖 v0.1.0-beta.1 Tag 或镜像，不合并 main。

## 根因与行为

- 公网 HTTP 不提供 crypto.randomUUID，createDialog/actionDialog 在打开前抛错。优先保留 randomUUID；缺少时用 getRandomValues 的16字节随机数构造 RFC4122 UUID v4。无 Math.random/时间戳降级，同一弹窗的任务ID重试时不变，独立弹窗生成新ID。服务端 Origin、CSRF、名称确认和一次性确认不变。
- UI 只在选择地区后查询实例，虽然 Linode/DO/Azure 已支持空地区查询。现在 Linode/DO 默认查询当前账号全部地区；Azure 仅当前资源组全部地区；AWS 保留明确地区要求，未查询时不显示虚假的总数。
- 登录后加载已有账号资源。切账号/地区及刷新时清除旧结果，数量在加载时为省略号、失败时为未知，失败可重试，避免把未查到当成0台。地区筛选可恢复“全部地区”。创建仍显式选择地区，生命周期操作仍使用实例自身地区。

## 回归证据

新增 scripts/browser-regressions.mjs：Chromium 将 narbe.test 解析到本机，以真实非secure HTTP环境运行，断言 isSecureContext=false、randomUUID不存在、getRandomValues存在。所有API为只读GET的UI测试替身，不是云端E2E。

修复前：HTTP创建弹窗等待超时；全地区London/Tokyo两实例未显示。两项独立复现均失败。
修复后：跨地区数量2→地区筛选1→全部2、切换空账号0→原账号2、创建地区/规格/镜像、生命周期确认弹窗、失败不显示旧数据、重试、桌面/移动端通过；DO/Azure范围与AWS地区约束分别验证。

原真实Go API/SQLite + Mock云浏览器测试额外禁用randomUUID，创建、响应丢失重试、删除确认均通过；断言UUID v4格式、重试ID相同、不同弹窗ID不同。完整 go test -race ./...、go vet、Prettier通过。

公网HTTP API限制依据：https://developer.mozilla.org/en-US/docs/Web/API/Crypto/randomUUID
兼容随机API依据：https://developer.mozilla.org/en-US/docs/Web/API/Crypto/getRandomValues

## 测试部署边界

Owner 测试机可以对已发布scratch镜像只读挂载本轮静态Linux AMD64程序到 /narbe，保持同一Compose项目和Volume。镜像本身仍为beta.1，不应把运行中的测试补丁伪称成GHCR已发布新版。部署前备份原compose、SQLite在线备份及master.key（备份仅服务器本地私有目录，不输出凭据）；校验无queued/running任务，更新后比对管理员密码摘要、凭据密文、主密钥、任务行和挂载Volume均保持一致。提供回滚脚本。安装器仍可使用同一Compose文件，保留只读程序挂载。

真实云验证仅包含已有Linode账号的地区、规格、镜像和实例只读查询；未执行真实创建、删除或生命周期写请求。下一公开版本需独立复审本轮应用diff，不能沿用beta.1已审核SHA冒充本轮PASS。

本轮 agy Embedded Reviewer 实际调用1次（gemini-3.8-flash-high/high），120秒超时无结论：0 completed / 1 failed / 0 re-review，不计正式审核。
