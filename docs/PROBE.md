# Narbe 国内连通性 Probe Agent（开发候选）

Probe Agent 是独立的 Go 静态程序。它只主动访问 Narbe 的 HTTPS 控制面，领取一次性租约并对指定公网 IPv4 执行有界 TCP、TLS、HTTP 或 HTTPS 只读检测。没有入站端口，不持有四云 API Token，不创建、删除、重启或修改云资源。

此文档对应开发候选。没有隐含已发布的 GHCR Probe 镜像；正式镜像地址与 Digest 必须由后续发行交付提供。已有 beta.5 Tag、镜像与发行证据不变。

正常 CI 成功后会分别提供 `narbe-probe-linux-amd64` 与 `narbe-probe-linux-arm64` Actions Artifact。它们是开发候选 Docker 归档，不是正式公开发行。选择与目标服务器相符的架构，下载解压后先核实 `SOURCE_SHA` 与本次候选提交一致，再校验、导入：

```bash
sha256sum -c SHA256SUMS
# ARM64；AMD64 改为对应的文件名及 :check 标签。
docker load --input narbe-probe-linux-arm64.tar
docker tag narbe-probe/ci:arm64 narbe-probe:cn-candidate
```

使用已导入镜像时，下述 Compose 命令用 `up -d --no-build`，避免重新编译。Artifact 保留 14 天；最终下载链接和对应源码 SHA 以本次工程交付为准。

## Docker 部署

前提：国内 Linux AMD64 / ARM64，或 macOS Docker Desktop；Docker Engine 与 Compose 可用。管理端必须已有可验证证书的 HTTPS 域名。不能使用 HTTP、跳过证书验证或自签证书绕过开关。

在本仓库中执行，先在 Narbe 探针管理页生成一次性注册凭据：

```bash
umask 077
read -r -s -p '一次性探针注册凭据: ' token; printf '\n'
printf '%s' "$token" > deploy/probe-registration-token.txt
unset token
# 容器固定使用非 root UID 65532；文件仅供该 UID 读取。
sudo chown 65532:65532 deploy/probe-registration-token.txt
sudo chmod 0400 deploy/probe-registration-token.txt
cat > deploy/.env <<'EOF'
NARBE_PROBE_CONTROL_URL=https://narbe.example.com
NARBE_PROBE_ALLOWED_PORTS=22,80,443,8443,8080
EOF
# 先将上面的 HTTPS 域名改为自己的管理端地址。
docker compose --env-file deploy/.env -f deploy/probe-compose.yaml up -d --build
docker compose --env-file deploy/.env -f deploy/probe-compose.yaml ps
docker compose --env-file deploy/.env -f deploy/probe-compose.yaml logs --tail=50
```

`probe_data` 保存绑定管理端 Origin 的探针身份，身份文件为 `0600`。注册成功后一次性凭据不再用于正常通信，重启沿用原 Volume。Compose 的 secret 文件仍须存在，确认完成注册后可将其内容替换为空文件并保留正确权限；若 Narbe 中删除了探针，需要新的注册凭据和新的独立身份 Volume。不要误删已有身份后复用已消费的凭据。

该容器启用只读根文件系统、非 root、全部 capability 移除、禁止提权、进程/内存限制、日志轮转和 `unless-stopped` 自动恢复。健康检查只证明最近两分钟控制面心跳成功，不代表国内可达性或云 API 授权成功。无效证书、网络错误、未注册或管理端拒绝凭据时不会显示健康。

运行检查的本地端口白名单最多 32 个，默认 `22,80,443,8443,8080`。管理端选择的端口还必须在此白名单中；空白名单或未允许端口失败关闭。部署者可以在 `deploy/.env` 精确修改，重新创建容器后生效；不要为“方便”允许全部端口。

## 出口网络与国内身份

Agent 和目标检测显式不继承 `HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`。要使用不同出口，请在宿主机或 Docker 的受控路由网络中部署独立探针；这不会配置 VPS 代理功能。

Agent 通过固定的 `https://api.country.is/`、验证 TLS 证书并强制 IPv4，获取当前进程实际观察到的公网 IPv4 与国家码。每次检测前后再次核对出口；变化或无法核实时返回“出口证据不足”，不能作为自动删机依据。Narbe 必须独立核验探针角色、期望出口与证据新鲜度：Agent 不会因为机器物理位置在国内就自行声明“中国大陆直连”；海外对照探针也可以使用相同程序。

Country.is [官方接口说明](https://country.is/) 说明 `GET /` 返回调用方 IP 国家，`GET /{ip}` 查询指定 IP；国家来自 GeoLite2 或 Cloudflare 地理数据。该结果属于第三方 IP 地理证据，不是物理位置或目标全链路路由证明。服务不可达、IPv6-only、代理/TUN 改变出口、地理证据不一致时应显示无法判断。

macOS Docker Desktop 容器通常经过 Linux 虚拟机 NAT，可能再经过宿主机 VPN / Clash / TUN。Linux Docker bridge 则通常经过宿主机 NAT；两者都不能仅从容器内网地址或宿主机所在地点判断国内出口。开启/关闭 VPN、TUN 或更改路由后，应检查管理端显示的实际出口与国家。按目标分流的 VPN 可能令地理服务和检测目标走不同路由，因此对严格国内直连需求，推荐独立国内 VPS 或禁用这类分流的受控网络，并核对出口。未实际运行的宿主机/网络组合不能列为验证通过。

## 检测安全与成功条件

- 只接受规范的公网 IPv4 字面量，拒绝 IPv6、映射地址、私网、回环、链路本地、CGNAT、文档/保留/组播地址，以及 Azure 平台虚拟地址 `168.63.129.16`。不解析目标域名，不访问 Metadata 地址。
- TCP/TLS/HTTP/HTTPS 连接始终固定为租约提供的同一个 IPv4 与允许端口，HTTP `Host` / TLS SNI 不改变连接地址。目标请求不走环境代理，不跟随任何重定向。
- TLS 使用正常证书验证与 TLS 1.2 最低版本。自签名、名称不匹配等属于 TLS 失败，不能等同于 IP 被墙。
- HTTP 默认要求 `200`，可指定其他 `2xx`；随机挑战开启时响应体必须精确等于挑战值。其他网站、重定向页面、错误页面不会被当作挑战成功。
- 每次超时限制 100ms–30s；目标响应体最多 16KiB，响应头最多 8KiB。无有效监听端口必须由管理端显示未准备好，不能通过任意超时推断被墙。
- Agent 单并发；心跳周期约 5s，故障后约 10s 重试，控制 HTTP 请求有超时与响应大小上限。一次任务最坏还包含前后出口核验时间，管理端租约须给出足够时间。
- Task ID、Lease ID、目标参数随结果原样相关联。最近 1024 个已领取租约持久保存，避免重放；服务端仍须永久核验任务/租约是否有效，不可依赖客户端的有限记录作为唯一防重放保护。
- 检测结果上传前落盘，上传失败则先重试同一结果，不领取新任务。服务端对完全相同的已接受结果进行幂等确认；若明确以 `410` 表示租约已过期或任务已停止，则丢弃旧结果继续心跳，不重复执行该租约。重启不会重发云写，因为探针根本没有云写能力。目标原始响应、认证凭据和任意服务端报错内容不会进入检测结果或日志。

## 控制面协议

所有接口仅在验证过的 HTTPS Origin 下发送 POST，禁止跨站重定向。注册后用独立探针 Bearer Token，不使用 Narbe 管理员 Session：

| 路径 | 请求 / 响应 |
| --- | --- |
| `/probe/v1/enroll` | `{registrationToken,version}` → `{probeId,token}`；注册凭据只能由服务端消费一次 |
| `/probe/v1/poll` | `{version,egressIP,egressCountry,egressObservedAt,egressSource,egressError?}` → `{task:null}` 或 `{task:{taskId,leaseId,target,timeoutMs}}` |
| `/probe/v1/results` | `{taskId,leaseId,target,result,egress}` → 2xx；服务端必须核验租约与目标完全一致 |

`target` 为 `{ip,port,protocol,path?,serverName?,expectedStatus?,challenge?}`。`result` 为 `{success,classification,latencyMs,startedAt,completedAt,statusCode?,error?}`。`egress` 为 `{ip,country,observedAt,source,error?}`。时间为 RFC3339；错误是静态、脱敏分类。管理端不得将单次 TCP 超时、TLS 错误或出口证据不足直接转换为可计费删除动作。

## 本地验证

```bash
go test -race ./internal/probe ./cmd/narbe-probe
go vet ./internal/probe ./cmd/narbe-probe
docker build -f Dockerfile.probe -t narbe-probe:cn-candidate .
NARBE_PROBE_IMAGE=narbe-probe:cn-candidate python3 scripts/test_probe_runtime.py
```

HTTP/TLS 合同测试在本地可信测试服务器和仅测试可用的注入 dialer 上执行；生产没有允许内网检测或跳过 HTTPS 校验的环境开关。测试替身通过不能证明真实国内被墙 IP、第三方地理服务、Linux / macOS TUN 路由或真实云资源操作已经通过验收。

Docker 运行回归脚本使用动态端口的本地 HTTPS 控制面测试替身和一次性 CA；CA 仅挂载进独立测试容器，Agent 仍执行正常证书及 SAN 校验。它验证单次注册、重启及强制重建身份不变、健康检查、非 root、只读、无入站、日志与资源限制。没有派发目标检测，更没有云写；出口观测仍是真实外网请求，服务失败时报告未知，而不会用替身伪造中国大陆出口。脚本要求本机 Docker 架构与镜像架构一致，只清理由本轮随机身份和 label 所有的容器与 Volume。

只读出口自检可独立运行，无需注册或检测任务：

```bash
# 宿主进程出口：
go run ./cmd/narbe-probe egress
# Docker 实际出口（不传任何云密钥，无入站端口）：
docker run --rm --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true narbe-probe:cn-candidate egress
```

输出包括真实观察到的 IP、国家、时间和来源。失败时返回非零退出码并输出明确错误 JSON。比较两份结果可以发现宿主机与 Docker Desktop 的出口差异；即使两者均为 `CN`，也不能证明具体目标或分流 VPN 的流量走相同线路。
