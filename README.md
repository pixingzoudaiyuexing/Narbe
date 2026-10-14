# Narbe

轻量、中文优先的单用户多云服务器工作台。Go + SQLite + 内嵌 Web，单个非 root Docker 容器，无 MySQL、Redis 或前端运行时。

**当前为第一阶段候选实现，正式独立审核待完成。** 四云适配器有自动化 HTTP 合同测试，Web 流程使用真实浏览器和真实应用/数据库验证、云端使用测试替身。没有真实云账号 E2E 证据。旧 CloudPanel 官方镜像不可达，不能声称已证明全部旧版功能覆盖。详见 [覆盖与缺口](docs/COVERAGE.md) 和 [验证报告](docs/VALIDATION.md)。

## 首个公开测试版本

Owner 已确认应用提交 `5ffbf3479ad1e079b4b35efcaae837ecf08f2b3b` 正式独立复审PASS，六项Finding全部关闭。本轮提供 `v0.1.0-beta.1`，不等同真实云E2E和完整旧版覆盖最终验收，也不宣称生产稳定。

无需服务器编译源码的GHCR安装包与说明见 [测试版安装](release/README.md)。发布安装包将版本锁定到准确digest，不使用latest；开发构建流程仍保留在下文。

## 安装

需要 Docker Engine/Desktop、Docker Compose v2、Buildx、Bash、OpenSSL、Python 3；仅源码开发需要 Go **1.26.9+**。支持 Linux amd64/arm64 源码构建；当前本机实际验证 linux/arm64，amd64 在 CI 构建。

```bash
git clone https://github.com/pixingzoudaiyuexing/Narbe.git
cd Narbe
# 当前候选实现位于待审核 PR 分支；main 仅为初始化基线
git switch codex/narbe-phase1
./scripts/start.sh
```

按提示设置至少 12 字符的独立管理员密码。默认仅监听宿主 `127.0.0.1:8080`，在浏览器打开 `http://localhost:8080`。初始密码从文件读取，不通过日志展示。已有数据库时不会用文件内容覆盖已修改的密码。

首次配置由 `.env.example` 复制为 `.env`。如需自定义密码文件，请先创建文件、设置 `NARBE_PASSWORD_FILE`，再启动；脚本不会覆盖或更改外部文件权限。容器 UID 65532 必须能读取该文件。默认密码文件位于权限 0700 的 `.local/` 下，文件 0644 是为了支持 Compose 的单文件只读 bind mount；不要将父目录开放给其他本机用户。

远程使用必须配置可信 HTTPS 反向代理，并将 `.env` 中 `NARBE_ORIGIN` 设为精确外部地址，例如 `https://narbe.example.com`。保持应用端口只绑定 loopback，或仅向受控代理开放。应用不信任客户端传入的 `X-Forwarded-*`，不会据此放宽来源检查。直接使用 IP/不同端口访问前，须同步修改 `NARBE_ORIGIN`。

若本机网络不能访问默认 Docker Hub / Go module proxy，可以仅对本次构建选择官方 Microsoft BuildKit 镜像和可达 Go proxy：

```bash
NARBE_BUILDKIT_IMAGE=mcr.microsoft.com/oss/v2/moby/buildkit:v0.34.0 \
NARBE_GOPROXY=https://goproxy.cn,direct ./scripts/start.sh
```

不会修改系统、Docker daemon、Git、npm 或其他项目代理设置。编译器镜像在 Dockerfile 按官方 digest 固定；最后一层为 scratch。Microsoft Go 的构建阶段使用 `nosystemcrypto` 选择 Go 标准加密实现以生成静态二进制，不依赖系统 OpenSSL，也不声称 FIPS 认证。

## 使用

1. 登录，在「云账号」中选择厂商，填写账号备注和凭据。保存时验证认证后的资源读取权限；公共地区列表不能证明凭据有效。
2. 进入「服务器」，刷新目录、选择地区、查询账号中的实例及公网 IP。
3. 创建时从官方目录选择规格和镜像。填写登录方式、网络等厂商特有配置，重新输入服务器名称后确认费用。
4. 实例操作依当前状态展示。删除、开关机、重启都需核实目标名称；删除无法撤销。
5. 在「操作任务」查看进度。等待中的任务可取消；已提交的云操作无法通过关闭浏览器撤销。
6. 未派发任务在应用重启时取消；正常等待或校验超过 5 分钟也取消，需重新检查配置并进行新的确认。系统时间异常产生未来时间戳时同样保守取消。已派发任务结果未知时先使用只读核实，再检查云控制台。仅在确认资源实际情况后，使用管理员密码记录人工核实并解除本地阻塞。该操作不会重发、重试或删除云资源。服务商标签可能延迟，未查到资源并不证明请求未发生。

### 四云差异

| 厂商 | 凭据 | 创建前置与登录方式 | 停止/删除边界 |
|---|---|---|---|
| AWS | Access Key ID / Secret / 可选 Session Token、验证地区 | 已有子网、安全组与 SSH key pair；子网和安全组同 VPC。动态 AMI 支持实际返回的 x86_64/ARM64。Windows 需 RSA key pair，密码在 AWS 控制台解密；Narbe 不接收私钥。 | 默认优雅停止；终止按云端磁盘 DeleteOnTermination 策略处理，务必先备份。重启仅报告请求受理。 |
| Azure | Tenant / Client / Secret / Subscription / Resource Group | 已有资源组及 VNet 子网；自动创建任务专属 Standard 公网 IP、NSG、NIC 和 OS disk，名称附带任务摘要。输入可信 IPv4 管理 CIDR，仅向其开放 SSH/RDP；已有子网策略仍可能限制访问。Linux 使用 SSH 公钥；Windows 使用管理员密码。 | 「关机」deallocate 释放计算；「关机并保留分配」powerOff 仍计计算费用。**删除仅删除 VM，保留磁盘及网络；保留资源继续可能计费。** 删除任务关联可信原创建账本（同账号版本、资源组、完整 VM ID 和当前任务标签），展示资源 ID 并标记存在及费用未核实；无法确认账本时明确显示未知，必须到 Azure 控制台检查。 部分创建也可能留下收费资源，任务显示完整计划路径，请在 Azure 控制台核实处理。不会删整个资源组。 |
| Linode | Personal Access Token | 动态地区及可用类型，root 密码或 SSH 公钥/账户密钥。IPv6 按厂商默认分配；可选收费备份。 | 删除同时删除实例磁盘和备份；独立 Volume、保留 IP 等可能继续存在和计费。重启仅报告请求受理。 |
| DigitalOcean | Personal Access Token | 动态地区、规格、镜像；已有账户 SSH key；可选 IPv6、收费备份和初始化脚本。不提供 Windows 镜像或原生指定密码字段。 | 默认优雅 shutdown；可能无法完成关机，任务等待真实 off。普通删除不隐式调用关联资源的危险批量删除 API。 |

Linode 官方 API 要求密码 7–128 字符并检查服务端强度分数，未公布评分算法。Narbe 保留更严格的 12 字符下限，拦截明显重复、连续序列、常见弱词及简单替换；这不保证通过云端强度评分，也不宣称固定“四类字符”是官方要求。随机密码、长随机短语和 SSH-only 均保留支持。

使用最小权限凭据：AWS 需要对应 EC2 Describe / RunInstances / Start / Stop / Reboot / Terminate 与 Tag 权限；Azure 应用限制在指定资源组并具备 Compute/Network 相关权限，同时需要订阅地区、规格及镜像读取；Linode/DigitalOcean 需要实例读写、镜像/地区/规格/SSH key/Tag 的相应权限。应用接入成功只证明已验证读取权限，不证明创建配额、容量或全部写权限。

镜像 ID 不写死。AWS 从可信发布者按支持的系统名称过滤 AMI，并处理完整分页、架构及废弃标记；Amazon Linux 可识别与展示，其维护期未建立权威动态映射时显示未确认；Azure 动态枚举官方发布者的 offer/SKU/精确版本并比较架构/Hyper-V，拒绝需 Marketplace plan 的镜像；Linode 检查状态、废弃及服务商 EOL/expiry；DO 检查镜像地区和最小磁盘。创建任务执行前重新查询。维护期依据 Debian 官方 distro-info-data 的 Ubuntu/Debian CSV 与 Microsoft 官方 Windows Server 发布文档，每 24 小时刷新，自动识别正常新增版本。默认优先标准维护与安全延长维护；维护结束和未发布版本默认隐藏，可显式展开。Ubuntu Pro/ESM、Debian 第三方 ELTS 不冒充普通维护。

**云厂商“可用”不等于发行版仍受安全维护。** 数据源不可达、无法识别的自定义镜像或其他发行版会标记“维护期未确认”，不会推导为已维护。维护窗口也不证明某个镜像已经安装最新补丁。Linode/DO 不伪造 API 未提供的 ARM64 元数据。当前仅 AWS/Azure 按实际镜像和规格元数据开放 ARM64。

## 升级、备份与恢复

```bash
# git pull 之前先确认本地改动；切到已经过审核的版本
./scripts/build.sh
docker compose up -d --no-build --wait
```

新镜像通过临时容器健康检查后才替换 `narbe/local:current`。旧镜像如果被运行/停止容器引用或带其他标签，会保留。构建脚本记录确切旧 current ID，Compose 替换后再次收集，防止连续升级累积悬空镜像；不会泛扫其他镜像。若明确需要回滚，可给旧镜像增加 `narbe/local:rollback-<版本>` 标签；脚本不会删除它。验证完成后由操作者检查引用并删除不再需要的确切回滚标签，不能无限保留。清理查询/回收失败会返回非零并写 `.local/cleanup-pending`，在核实此前资源前拒绝新增构建。异常进程终止后如有 `.local/build.lock` / `collect-images.lock`，先确认无对应构建进程，再解除本地锁；不要盲目删除未知锁。

`narbe_data` Volume 保存 SQLite 数据库、WAL 与 `master.key`。**不要执行 `docker compose down -v`，不要删除此 Volume 或更换主密钥。** 停止应用后完整备份 `/data`，包括数据库和主密钥；备份必须加密并限制访问。恢复时停止应用、整体恢复匹配的一套数据并保持 UID 65532 可写。密钥缺失/不匹配会拒绝启动；密钥丢失无法从数据库恢复云凭据。当前不提供在线主密钥轮换。

应用启动创建 schema v1；同一数据目录只允许一个进程。未来升级如果引入不兼容迁移，必须先备份并按对应版本说明执行。不要将数据目录挂载到不支持可靠 SQLite 锁和 fsync 的网络文件系统。

容器日志限制为 5 MB × 3，级别仅 info/warn/error，调试日志无默认入口。应用不记录云请求正文、凭据、登录密码。直接运行源码时请由服务管理器管理 stdout 日志轮转。任务历史保留在数据库；界面展示最近 200 条，不承诺无限数据量占用恒定空间。

## 开发与验证

```bash
go test ./...
go test -race ./...
go vet ./...
python3 scripts/test_build.py
npm ci --ignore-scripts
npx playwright install chromium
NARBE_BROWSER_TEST=1 go test ./internal/app -run TestBrowser -v
./scripts/build.sh
python3 scripts/test_runtime.py
# 真实一键安装验证，临时独立 Compose 项目，无云凭据
python3 scripts/test_install.py
```

macOS 浏览器验证默认使用已安装的 Google Chrome；其他环境使用 Playwright Chromium，或通过 `NARBE_BROWSER_EXECUTABLE` 指定可执行文件。Playwright/Prettier 仅为开发依赖，不进入生产镜像。`test_runtime.py` 只操作新建且带 disposable 标签的验证容器和验证 Volume；不接入云账号。

单元/合同测试不连接真实云；浏览器测试运行真实 Go API/SQLite，云服务替身通过测试代码注入，生产程序没有 mock 或跳过认证的开关。所有真实收费创建和云资源删除，需要另行获得 Owner 对目标、数量和操作的授权。

更多说明：[架构与安全边界](docs/ARCHITECTURE.md)、[功能覆盖](docs/COVERAGE.md)、[内部审查](docs/REVIEW.md)、[实际验证](docs/VALIDATION.md)。
