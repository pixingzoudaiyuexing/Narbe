# Narbe v0.1.0-beta.5 安装、升级与回滚

**发行准备稿：尚未发布，下面的 beta.5 下载地址需等待正式发行后才可用。** 应用正式审核 PASS WITH CONDITIONS，应用 SHA 固定 `182e57759494c6fbcf6e3291d8259b524a9ea49a`，已审交付证据 HEAD 为 `800c5443cb5fb913e30fe71467642c6fdfa87b00`。发行配置独立复审 REQUIRED / PENDING，本阶段不设置两个 beta.5 审核变量，不执行 GHCR／Tag／Release 发布。

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

Linode 重装支持不提交新脚本、Shell、Cloud-init、已有 StackScript 和动态 UDF。会删除所有磁盘及配置，必须备份并确认名称；省略新 Metadata 不保证旧 User Data 延续。脚本/UDF 绑定确认、加密持久化，不改变原防火墙和接口。API 受理、开机不等于脚本执行成功；可登录来宾检查 `/var/log/cloud-init.log`、`/var/log/cloud-init-output.log`，StackScript 依脚本自身日志。现有四云创建脚本能力保留，Azure Windows 本版仍不提供自动初始化脚本执行。

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

先在**实际旧安装目录**执行 `pwd -P`、上面的 `ps`，用容器 Inspect 核实 `com.docker.compose.project`、工作目录、`/data` 的 Volume 和配置。先处理 queued/running 任务；重启会取消 queued，running/unknown 仍需核实，不能借升级重放云写。

停止应用后完整备份 `/data`（SQLite、WAL、master.key 一起）和部署文件、密码文件；备份须保密。确认实际项目和 Volume 后，在新目录下载、验证 beta.5 安装包，复制原 `.env` 与密码文件，保留原 Compose 项目、数据 Volume、端口和 Origin。自定义 Compose 必须逐项保留配置，仅更新经验证的 `image.env` 镜像引用；不要用包内默认配置覆盖未知改动或创建空 Volume 冒充升级。

标准未自定义安装可在核实后按以下步骤操作；路径必须换成实际绝对路径：

```bash
(
set -euo pipefail
old_dir=/替换为实际/beta4安装目录
new_dir=/替换为新的/beta5目录
backup_dir=/替换为新的/私有升级前备份目录
cd "$old_dir"
docker compose --env-file .env --env-file image.env -f compose.yaml stop
mkdir -m 700 "$backup_dir"
docker compose --env-file .env --env-file image.env -f compose.yaml cp narbe:/data "$backup_dir/data"
cp .env image.env compose.yaml "$backup_dir/"
mkdir -m 700 "$backup_dir/secrets"
cp secrets/admin-password "$backup_dir/secrets/admin-password"
mkdir -m 700 "$new_dir" && cd "$new_dir"
curl -fL --retry 3 -o narbe-v0.1.0-beta.5-install.tar.gz https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.5/narbe-v0.1.0-beta.5-install.tar.gz
curl -fL --retry 3 -o SHA256SUMS https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.5/SHA256SUMS
sha256sum -c SHA256SUMS
tar -xzf narbe-v0.1.0-beta.5-install.tar.gz
cp "$old_dir/.env" .env
mkdir -m 700 secrets
cp "$old_dir/secrets/admin-password" secrets/admin-password
./install.sh
docker compose --env-file .env --env-file image.env -f compose.yaml ps
)
```

核对原密码登录、账号列表、SQLite、master.key、项目/Volume、端口和 Origin；验证前不删除旧镜像或升级前备份。密码文件父目录保持 0700，单文件 0644 只读挂载便于 UID 65532 读取；不得公开上传配置、密码、数据库或 key。

## 回滚限制

**beta.4 不认识 beta.5 的云 API 代理设置。保留 beta.4 镜像不等于已验证 beta.5 数据可无损回滚。** beta.4 可能使用旧版出口行为，不能假定仍经 beta.5 代理。

本轮验证的是 beta.4→beta.5，不是升级后数据库的反向兼容。必要回滚时先停止 beta.5 并另存升级后数据，依据完整的**升级前 beta.4 备份**恢复到隔离 Volume 核实，再由操作者决定切换原项目。只还原旧镜像不保证兼容；不要覆盖或删除唯一数据库、master.key，不能在有未核实云写时自动回滚或重试。

禁止 `docker compose down -v`、删除正式 Volume、全局 prune、丢弃／替换唯一 master.key。缺少原 key 不能解密凭据。安装器不执行云创建、重装、删除或资金操作。

## 来源与发行证据

正式安装包名称 `narbe-v0.1.0-beta.5-install.tar.gz`，附件包括 `SHA256SUMS` 和 `release-evidence.json`。镜像为 `ghcr.io/pixingzoudaiyuexing/narbe:v0.1.0-beta.5@sha256:实际发行digest`，不能使用占位符安装。

包内 `release.json` 与外部证据记录应用审核 SHA、已审交付证据 SHA、发行审核 SHA、实际源码 SHA、Index／两架构 Manifest Digest、Actions URL 及来源门禁分类。发行审核后仅允许 release/README.md、release/NOTES.md 普通文案后补；未审核执行差异失败关闭。实际安装可用性以完成正式复审后的工作流、公开镜像和 Release 附件验收为准。
