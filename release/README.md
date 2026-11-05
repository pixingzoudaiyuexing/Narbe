# Narbe v0.1.0-beta.5 安装、升级与回滚

**Owner 已授权，发行执行中；是否已发布以实际 GitHub Pre-release、成功工作流和公开附件为准。** Owner 于 2026-11-05 明确免除本轮额外人工独立复审；本次整合及发行不宣称 Independent Reviewer PASS。最终安装包固定精确源码 SHA，并要求同 SHA 的完整 CI、原生双架构运行、回拉及升级验收成功。

Go + SQLite、单个非 root 容器，支持 Linux AMD64 / ARM64 和 AWS、Azure、Linode、DigitalOcean。需要 Docker Engine、Compose v2、Bash、curl、tar、sha256sum；服务器无需 Go、Node.js、MySQL 或 Redis，不自动修改系统 Docker。测试版不宣称生产稳定。

## 首次安装（正式发行后）

在新目录下载安装包并校验，不执行 curl 管道脚本：

```bash
mkdir -m 700 narbe-beta5 && cd narbe-beta5
curl -fL --retry 3 -o narbe-v0.1.0-beta.5-install.tar.gz https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.5/narbe-v0.1.0-beta.5-install.tar.gz
curl -fL --retry 3 -o SHA256SUMS https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.5/SHA256SUMS
sha256sum -c SHA256SUMS
tar -xzf narbe-v0.1.0-beta.5-install.tar.gz
./install.sh
```

首次设置至少 12 字符管理员密码，不回显。安装器只拉取 `image.env` 中版本＋digest 锁定的 GHCR 镜像，不编译、不使用 latest。数据库已存在时不会用 bootstrap 文件覆盖改过的密码。

默认绑定 `127.0.0.1:8080`，访问 `http://localhost:8080`。远程可使用 SSH 隧道 `ssh -L 8080:127.0.0.1:8080 your-user@your-server`。需要公开远程访问时使用可信 HTTPS 反向代理，保持管理端口不直接暴露公网，并将 `.env` 的 `NARBE_ORIGIN` 设为精确外部 origin；不信任 X-Forwarded-* 放宽认证。不自动改动 Owner 现有 HTTP／HTTPS 配置。

## 网络出口与重装

设置页可选显式直连或 HTTP / HTTPS / SOCKS5 / SOCKS5H 代理，密码加密且不回显，编辑须保留／替换／明确清除。新网络配置取消旧 queued、作废旧确认；running 使用原确认出口，unknown 不重放，不自动绕过代理直连。

三项出口 IP 检测相互独立：浏览器不传 Narbe 会话或云凭据，服务器直连不读取环境代理，代理检测使用与云 API 相同配置。IP 成功只证明本次检测，云 API 是否授权须另做只读诊断。HTTP／未加密 SOCKS 不保证代理认证保密，公网推荐 HTTPS 或安全隧道；Go 的 SOCKS5 与 SOCKS5H 均由代理解析目标域名。代理不修改新 VPS 的系统网络。

默认拒绝环回、私网、Metadata 和保留地址。确需私网代理时，服务器管理员必须在自己的 Compose `environment` 中显式加入 `NARBE_PROXY_ALLOW_PRIVATE` 精确 RFC1918/ULA IP:端口授权，例如 `10.2.3.4:8443`；禁止任意网段、loopback 或 Metadata。仅在 `.env` 增加未声明变量不会自动注入容器。本安装包不默认开放任何私网代理地址。

Linode 重装支持不提交新脚本、Shell、Cloud-init、已有 StackScript 和动态 UDF。会删除所有磁盘及配置，必须备份并点击一次明确风险确认；省略新 Metadata 不保证旧 User Data 延续。脚本/UDF 绑定确认、加密持久化，不改变原防火墙和接口。API 受理、开机不等于脚本执行成功；可登录来宾检查 `/var/log/cloud-init.log`、`/var/log/cloud-init-output.log`，StackScript 依脚本自身日志。现有四云创建脚本能力保留，Azure Windows 本版仍不提供自动初始化脚本执行。

真实云账号经生产代理的授权查询、第三方代理兼容、Linode Rebuild 后 Metadata / Cloud-init 执行及私人 StackScript IAM 尚未真实 E2E。合同替身、匿名 400/401、浏览器和 Docker 测试不能替代真实云验证；九项高级操作与原 CloudPanel 完整覆盖旧缺口继续保留。脚本权限较高，不要硬编码 Token 或私钥。新建 Linode 默认全部放行云防火墙，不改 OS 防火墙或已有绑定；Azure VM 删除后关联资源可能继续计费。

## 状态、停止、重启

在实际安装目录中使用同一项目、环境文件及 Compose 配置：

```bash
docker compose --env-file .env --env-file image.env -f compose.yaml ps
docker compose --env-file .env --env-file image.env -f compose.yaml logs --tail=100 narbe
docker compose --env-file .env --env-file image.env -f compose.yaml stop
docker compose --env-file .env --env-file image.env -f compose.yaml start
docker compose --env-file .env --env-file image.env -f compose.yaml restart
```

可再次运行 `./install.sh`，不覆盖已有数据库或密码文件。日志 5MB×3，禁止长期 debug 或记录凭据。默认项目 `narbe`、Volume `narbe_narbe_data`；实际身份以原部署为准，不能假设示例就是 Owner 环境。

## beta.4 → beta.5（正式发行后）

**不要把新包解压覆盖旧目录，也不要在新目录启动一个不同项目。** 只更新原目录的 `image.env`，继续使用原 Compose、`.env`、secrets、项目名和数据 Volume；这样保留端口、Origin、管理员密码及加密云账号。执行前先处理 queued/running，结果未知的云写必须核实；重启取消 queued，不自动重放 unknown。

下面命令适用于安装包原有的单一 `compose.yaml`，会要求输入真实目录和现有容器 ID，核对项目／Volume，并停止后完整备份。它不假定 Owner 的路径。**存在自定义多个 Compose 文件、额外 env-file、`COMPOSE_*` 环境变量或 bind-mount 数据目录时，命令会拒绝；应保留原部署命令的全部参数执行等价流程，不能套用默认文件覆盖未知配置。** 自定义 Compose 必须实际使用 `NARBE_IMAGE`；硬编码 image 时须由操作者只修改镜像引用并核对最终渲染配置。

```bash
(
set -euo pipefail
umask 077
read -r -p '实际 beta.4 安装目录（绝对路径）: ' old_dir
cd "$old_dir"
old_dir=$(pwd -P)
test -f .env && test -f image.env && test -f compose.yaml
test ! -L image.env
# 未知 Compose 环境可能更换项目、配置或覆盖顺序，先失败关闭。
if env | grep -q '^COMPOSE_'; then
  echo '检测到 COMPOSE_*；请使用已核实的原部署命令进行等价升级。' >&2; exit 1
fi
docker ps -a --filter label=com.docker.compose.service=narbe --format 'table {{.ID}}	{{.Names}}	{{.Image}}'
read -r -p '上方现有 beta.4 Narbe 容器 ID: ' selected
container=$(docker inspect --format '{{.Id}}' "$selected")
project=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}' "$container")
workdir=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.working_dir"}}' "$container")
configs=$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project.config_files"}}' "$container")
test -n "$project" && test "$workdir" = "$old_dir"
if test "$configs" != "$old_dir/compose.yaml"; then
  echo '不是单一标准 compose.yaml；保留原配置文件与所有参数，不自动改动。' >&2; exit 1
fi
compose=(docker compose --project-directory "$old_dir" -p "$project" --env-file .env --env-file image.env -f compose.yaml)
actual=$("${compose[@]}" ps -aq narbe)
test -n "$actual" && test "$(docker inspect --format '{{.Id}}' "$actual")" = "$container"
volume=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{if eq .Type "volume"}}{{.Name}}{{end}}{{end}}{{end}}' "$container")
test -n "$volume"
printf '原目录=%s\n项目=%s\n数据 Volume=%s\n' "$old_dir" "$project" "$volume"
"${compose[@]}" ps
# 只显示非敏感的 Origin；不打印密码、数据库或完整环境。
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$container" | sed -n '/^NARBE_ORIGIN=/p'
read -r -p '已核实上述身份，且已处理运行中／未知云任务？输入 yes: ' confirmed
test "$confirmed" = yes
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
curl -fL --retry 3 -o "$stage/narbe-v0.1.0-beta.5-install.tar.gz" https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.5/narbe-v0.1.0-beta.5-install.tar.gz
curl -fL --retry 3 -o "$stage/SHA256SUMS" https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.5/SHA256SUMS
(cd "$stage" && sha256sum -c SHA256SUMS)
tar -xzf "$stage/narbe-v0.1.0-beta.5-install.tar.gz" -C "$stage" image.env
test "$(wc -l < "$stage/image.env")" -eq 1
grep -Eq '^NARBE_IMAGE=ghcr[.]io/pixingzoudaiyuexing/narbe:v0[.]1[.]0-beta[.]5@sha256:[0-9a-f]{64}$' "$stage/image.env"
# 先拉取已验包中的不可变镜像；此时仍未更改原配置。
image=$(sed -n 's/^NARBE_IMAGE=//p' "$stage/image.env")
docker pull "$image"
backup=$(mktemp -d "$(dirname "$old_dir")/narbe-before-beta5-$(date +%Y%m%d-%H%M%S)-XXXXXX")
chmod 700 "$backup"
"${compose[@]}" stop narbe
"${compose[@]}" cp narbe:/data "$backup/data"
cp -a .env image.env compose.yaml secrets "$backup/"
printf '完整升级前备份：%s\n' "$backup"
# 不改 Compose、.env、secrets 或正式 Volume，只替换镜像锁。
cp "$stage/image.env" image.env
rendered=$("${compose[@]}" config --images)
if test "$rendered" != "$image"; then
  cp "$backup/image.env" image.env
  echo '原 Compose 未采用预期镜像；已还原镜像锁，服务保持停止，请核实自定义配置。' >&2; exit 1
fi
"${compose[@]}" up -d --no-build --wait narbe
new_container=$("${compose[@]}" ps -q narbe)
new_volume=$(docker inspect --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{if eq .Type "volume"}}{{.Name}}{{end}}{{end}}{{end}}' "$new_container")
test "$new_volume" = "$volume"
"${compose[@]}" ps
"${compose[@]}" logs --tail=100 narbe
printf '请用原管理员密码登录，核对云账号、端口与 Origin。保留升级前备份：%s\n' "$backup"
)
```

命令只拉取发布镜像，不创建／重装／删除云资源。失败时不要删除备份或 Volume；停止后的失败不会自动启动另一版本，更不会自动重试云任务。升级完成前不删除 beta.4 镜像。密码目录保持 0700、挂载文件按原部署权限保留，不公开上传配置、密码、数据库或 key。

## 并发、自动改密和密码输入

四个 Worker 并发运行，unknown 不锁账号。普通操作直接提交，删除／重装只保留一次明确风险按钮；同请求 ID 幂等、Session、CSRF、资源和参数绑定保持。相同服务器的并发操作由云 API 判断，事件归属无法唯一核实时保守 unknown，不添加本地服务器互斥。

Linode root 密码重置自动核实实时状态，必要时关机并确认，重置同一目标盘并确认密码事件，再恢复原有运行状态。原本关机则不自动开机，未知步骤停止后续风险写；任务详情展示各阶段和核实建议。云新密码默认明文、可用眼睛按钮隐藏，不改变强度校验、密文任务及结果脱敏。默认可见存在旁观风险，不应共享屏幕输入真实密码。

**真实 Linode 自动改密流程尚未云端 E2E。** 隔离替身证明行为与异常路径，不能替代实际账号权限、事件形态和云时延验收。

## 回滚限制

**beta.4 不认识 beta.5 的云 API 代理设置。保留 beta.4 镜像不等于已验证 beta.5 数据可无损回滚。** beta.4 可能使用旧版出口行为，不能假定仍经 beta.5 代理。

本轮验证的是 beta.4→beta.5，不是升级后数据库的反向兼容。必要回滚时先停止 beta.5 并另存升级后数据，依据完整的**升级前 beta.4 备份**恢复到隔离 Volume 核实，再由操作者决定切换原项目。只还原旧镜像不保证兼容；不要覆盖或删除唯一数据库、master.key，不能在有未核实云写时自动回滚或重试。

禁止 `docker compose down -v`、删除正式 Volume、全局 prune、丢弃／替换唯一 master.key。缺少原 key 不能解密凭据。安装器不执行云创建、重装、删除或资金操作。

## 来源与发行证据

正式安装包名称 `narbe-v0.1.0-beta.5-install.tar.gz`，附件包括 `SHA256SUMS` 和 `release-evidence.json`。镜像为 `ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.5@sha256:实际发行digest`，不能使用占位符安装。

包内 `release.json` 与外部证据明确记录 `releaseMode=owner-authorized-manual-review-waiver`、Owner 2026-11-05 免人工复审授权、实际 `sourceSha`／`authorizedSourceSha`、已验证应用基线 `3216f1e9e5151338427c7ceee5fe0090fdebf1e4`、整合 PR #2 HEAD `ec66fd4e90f38a1940b8cdf662868bd1a5a0a70e`、同源码成功 CI、Index／两架构 Manifest Digest 和发行 Actions URL。新源码不填写虚假的 `applicationReviewedSha` 或 `releaseReviewedSha`。构建只接受固定授权源码且无额外／忽略构建输入，GHCR 写权限受来源和成功测试约束。公开可安装状态以成功发行工作流、公开镜像和实际可下载附件为准。
