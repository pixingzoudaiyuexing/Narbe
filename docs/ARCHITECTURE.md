# 架构与安全边界

单进程 Go HTTP server + 内嵌原生 ES modules/CSS + SQLite WAL。云厂商在 `internal/cloud` 各自独立实现 Provider；业务、认证、确认与 worker 在 `internal/app`；加密和数据库在 `internal/store`。没有微服务、消息代理、商业化或多租户模块。

## 数据

schema v1 包含 settings、sessions、accounts、jobs、confirmations。当前只有一个管理员身份，不以一个虚假的 tenant 参数提供安全承诺；未来多用户需明确迁移、所有权字段与授权设计，不复用当前单用户假设。

* AES-256-GCM，随机 nonce，AAD 绑定账号/任务 ID。账号凭据、包含 root 密码或初始化脚本的任务载荷加密。
* `master.key` 32 字节随机生成，0600；目录 0700、数据库 0600。缺失或不匹配密钥拒绝启动；settings 中的 GCM sentinel 先验证匹配，再恢复任务状态。
* SQLite WAL、foreign_keys、busy_timeout、synchronous=FULL；进程级 `.lock` flock 防止第二个实例改写任务恢复状态。
* 会话只存随机令牌的 SHA-256 摘要，12 小时有效。Argon2id 密码：64 MiB、2 iterations、2 lanes；使用标准 constant-time compare。登录按来源限速（10 分钟最多 8 次），哈希并发上限 2。来源表最多 4096 条；容量满时清理/淘汰，优先保留受限来源，不能以全局容量拒绝合法新来源。有限表不等于完整抗无限来源轮换。
* 移除账号采用 retirement，清空当前凭据密文并保留历史任务。历史/备份并非安全擦除；数据库和 key 都须受保护。

## 认证和请求

除静态前端和最小 healthz 外，API 全部要求数据库会话。修改请求要求精确 Origin + 会话 CSRF。Cookie HttpOnly/SameSite=Strict，HTTPS origin 时 Secure。密码更改撤销全部会话。没有默认口令、外部脚本、跨域 API、任意 endpoint 配置或自动信任代理头。

请求体 128 KiB、头 16 KiB；严格 JSON 类型和未知字段校验。云响应读取有上限。所有 SQL 参数化、界面显示转义、CSP self-only。云 HTTP 禁止重定向；Azure 分页和 LRO 只接受 HTTPS management.azure.com 和当前订阅。错误按已知原因翻译，不返回原始云响应、密码或 Token。

## 修改与可靠任务

1. 确认读取当前云目录/实例身份，绑定账号 revision、地区、目标、全部配置、请求 ID 和用户输入的名称，生成 5 分钟一次性摘要令牌。
2. 提交事务消费令牌、重新核对账号 revision/retirement、检查该账号无 unresolved job 并插入 encrypted intent。确认期限在入队时核验；未派发任务还有独立的 5 分钟等待/校验期限。恢复时取消 queued 并作废旧确认，running 仍变为 unknown。重新操作需要新请求 ID 和新的确认。
3. 相同 ID 与规范化载荷返回既有任务；载荷不同返回冲突。前端在一次对话框内固定 intent ID，响应丢失也保留原 ID。
4. worker 先取消过期或未来时间戳 queued；只有年龄有效的任务可以原子 claim queued→running，落盘后才允许出站。只读预检完成后及首个云写 HTTP 请求前再次检查有效期，以带单调时间的截止保护防时钟回拨延长授权。已启动的多步操作按原操作 context 完成，不因排队期限中断 Azure 后续依赖步骤。再次核对 live 参数及凭据 revision；Azure 全部计划 ID 在第一笔 PUT 前保存。
5. AWS 使用任务 ID 作 ClientToken；DO/Linode 随创建提交 task tag；Azure 由摘要确定资源 ID，先检查不存在，绝不覆盖未知已有资源。
6. 保存云端操作 ID/LRO 和关联资源后查询终态。AWS/Linode 没有可靠重启完成证据时只报告“请求受理”。Azure LRO 区分 AsyncOperation/Location 并遵守 Retry-After。DO action 完成后仍检查目标实例状态。
7. 网络中断、响应不完整、部分 Azure 依赖成功、等待超时或恢复时的 running→unknown；永不自动重发已派发写请求。
8. unknown 可只读核实已知操作目标；缺少可证明结果时保持阻塞。管理员可以在控制台核实后，重新验证密码/目标名并明确确认，记录 resolved 解除本地阻塞。它不等于证明云写操作成功/失败，也不重发任何操作。

单 worker、单账号最多一个未解决任务，优先可靠性与轻量。不能取消云端已接受操作；只能取消尚未 claim 的 queued。数据库故障会保持运行/未知状态阻塞，不用自动重试掩盖。

## 已知边界

没有可信云凭据，真实 E2E 尚未验证。Azure 删除保留数据磁盘/OS disk/网络，可能继续收费；关联资源清理必须在云控制台核实，不实现盲目自动回滚。需要进一步验证 Azure 所有地区/SKU/LRO 变种、AWS 账户与分区差异、provider IAM/capacity。

目前 AWS 为商业分区；没有实现中国/GovCloud 分区端点。AWS 使用已启用地区。生命周期通过官方公开 CSV / Markdown 自动刷新；未知来源、数据不可达或发行版未识别时标记 unknown，不能把 cloud available 等价为安全维护。Ubuntu ESM / Debian ELTS 不默认纳入普通维护。

Go 内存与浏览器 DOM 中的短期明文不能保证物理清零，数据库与 master.key 同时泄露仍可解密。Azure 删除关联原创建账本时重新核对同账号版本、当前 subscription/RG、完整资源 ID 与云端 `narbe-job` 标签；删除前持久化四个关联 ID 和来源任务，成功/失败/unknown 都保留。账本只说明来源，资源存在及费用未核实；无可信来源或多重来源时 unknown，不暴露未校验 ID，也不删除依赖。

当前不提供 MFA、主密钥在线轮换或自动删除收费残留；这些边界需要正式审核确认，不能据此声称安全认证完成。
