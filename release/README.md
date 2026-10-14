# Narbe v0.1.0-beta.2 安装与使用

这是 beta.2 公开测试版安装说明。正式独立审核基线 `e7aaadef8cf1e34a08b9b345fc3e8ba3970b6c72` 已获 PASS WITH CONDITIONS — RELEASE APPROVED；发布工作流核对受保护代码与审核SHA一致，并原生验证两个架构。已发布 beta.1 不变。本版供 Owner 自行接入云账号测试，不宣称生产稳定。支持 Linux AMD64 / ARM64、AWS / Azure / Linode / DigitalOcean，单个 Go + SQLite 容器。

需要已安装 Docker Engine 和 Docker Compose v2、Bash、curl、tar、sha256sum；服务器无需 Go、Node.js、MySQL 或 Redis。不会自动安装或改动系统 Docker。

## 一键安装

在一个新目录中下载并验证发布安装包，不执行 curl 管道脚本：

```bash
mkdir -p narbe-beta && cd narbe-beta
curl -fL --retry 3 -o narbe-v0.1.0-beta.2-install.tar.gz https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.2/narbe-v0.1.0-beta.2-install.tar.gz
curl -fL --retry 3 -o SHA256SUMS https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.2/SHA256SUMS
sha256sum -c SHA256SUMS
tar -xzf narbe-v0.1.0-beta.2-install.tar.gz
./install.sh
```

按提示输入并确认至少12字符的独立管理员密码，输入不会显示。安装器只拉取 `image.env` 中版本＋digest锁定的GHCR镜像，不编译源码、不使用 latest。

首次在浏览器打开 `http://localhost:8080`，用刚设置的密码登录。默认只向宿主 `127.0.0.1:8080` 绑定，不能从公网直接访问。云账号凭据在网页中由 Owner 自己填写；发布与安装测试没有使用真实云凭据。

## 远程访问

测试可以使用SSH加密隧道：

```bash
ssh -L 8080:127.0.0.1:8080 your-user@your-server
```

然后从本地浏览器访问 `http://localhost:8080`。如果本地端口更换，应把 `.env` 的 `NARBE_PORT` / `NARBE_ORIGIN` 与实际访问origin保持一致。

正式远程访问推荐使用已有的HTTPS反向代理：应用仍绑定loopback，代理转发到 `http://127.0.0.1:8080`；为自己的域名配置有效TLS证书，设置 `.env` 中 `NARBE_ORIGIN=https://你的域名`，随后运行 `./install.sh`。不要把未经TLS保护的8080直接绑定公网。应用不信任 X-Forwarded-* 来放宽认证或来源检查。反向代理不包含在本单容器安装包中，可复用宿主已有服务。

## 状态、日志与重启

始终使用同一组环境文件，以保留digest锁定：

```bash
docker compose --env-file .env --env-file image.env -f compose.yaml ps
docker compose --env-file .env --env-file image.env -f compose.yaml logs --tail=100 narbe
docker compose --env-file .env --env-file image.env -f compose.yaml stop
docker compose --env-file .env --env-file image.env -f compose.yaml start
docker compose --env-file .env --env-file image.env -f compose.yaml restart
```

也可以再次运行 `./install.sh`；它不会覆盖已有密码文件或数据库密码。日志为5MB×3轮转，不开启永久debug，不记录云密码或Token。

## 数据与升级

固定Compose项目名为 `narbe`，默认持久化Volume为 `narbe_narbe_data`，包含SQLite数据库、WAL和 `master.key`。容器替换、重启或同版本重新安装不会清空数据。

**禁止 `docker compose down -v`、删除正式Volume或替换主密钥。** 缺失/不匹配主密钥会拒绝启动，丢失原密钥不能解密云凭据。

升级到未来明确审核的版本时：停止应用并完整备份该Volume（包括数据库和master.key，备份应加密）；在新版本目录下载、校验完整安装包，复制原 `.env` 和 `secrets/admin-password`，保持固定项目名/同一数据Volume，运行新安装器。新 `image.env` 必须属于该明确版本的digest，不跟随latest。不要覆盖旧目录中的未知改动；升级后验证再检查旧镜像引用关系，回收不需要的旧镜像。不要全局prune或删持久化数据。

已有数据库时，bootstrap密码文件不覆盖数据库内改过的管理员密码。密码文件放在0700私有目录下，0644单文件只读挂载使容器UID65532可读；不要开放其父目录。不要把 `.env`、密码文件、数据库和key公开上传。

## 测试版边界

beta.1 的六项审核问题已修复并正式复审PASS。本轮新增四云资源总览、中文地区、规格/价格说明和HTTP弹窗修复已完成正式独立审核（PASS WITH CONDITIONS — RELEASE APPROVED）。确切构建与审核SHA记录在release.json；发布后的文档提交仅允许README/Notes文字差异，不修改已审核应用或可执行包装。

支持动态地区、规格和系统镜像查询，已有实例查询、创建及生命周期管理。云厂商差异、计费、配额及权限必须由 Owner 自行验证。

* 四云真实创建/删除E2E尚未完成，Mock和本地Docker成功不等于真实云验证。
* 原CloudPanel完整功能覆盖仍有证据缺口。
* Azure删除只删除VM，磁盘、网卡、NSG、公网IP可能保留并继续产生部分费用。任务账本只确认来源，不证明资源当前存在或费用已停止；需到控制台核实。
* 重启取消尚未派发的任务；未派发任务等待/校验最多5分钟。结果未知的已派发操作不会自动重放。

版本、构建SHA、index与两个平台manifest digest记录在 `release.json` 和 Release 附件 `release-evidence.json`。

## beta.1 无损升级到 beta.2

先在原安装目录停止服务，再完整备份 `narbe_narbe_data` Volume（SQLite及master.key必须一起备份，备份文件妥善保密）。不要运行 `down -v`。在新的beta.2目录下载、校验安装包；以下示例假设旧目录为同级 `narbe-beta`，复制既有配置与密码文件：

```bash
(
set -euo pipefail
# 在旧目录停止，保留容器和数据Volume
cd narbe-beta
docker compose --env-file .env --env-file image.env -f compose.yaml stop
backup_dir="../narbe-backup-$(date +%Y%m%d-%H%M%S)"
mkdir -m 700 "$backup_dir"
docker compose --env-file .env --env-file image.env -f compose.yaml cp narbe:/data "$backup_dir/data"
cp .env "$backup_dir/deploy.env"
mkdir -m 700 "$backup_dir/secrets"
cp secrets/admin-password "$backup_dir/secrets/admin-password"
cd ..
mkdir -p narbe-beta2 && cd narbe-beta2
curl -fL --retry 3 -o narbe-v0.1.0-beta.2-install.tar.gz https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.2/narbe-v0.1.0-beta.2-install.tar.gz
curl -fL --retry 3 -o SHA256SUMS https://github.com/pixingzoudaiyuexing/Narbe/releases/download/v0.1.0-beta.2/SHA256SUMS
sha256sum -c SHA256SUMS
tar -xzf narbe-v0.1.0-beta.2-install.tar.gz
cp ../narbe-beta/.env .env
mkdir -m 700 secrets
cp ../narbe-beta/secrets/admin-password secrets/admin-password
./install.sh
docker compose --env-file .env --env-file image.env -f compose.yaml ps
)
```

使用固定Compose项目名 `narbe` 连接同一Volume，保留原管理员密码和凭据。不覆盖 `.env` 中的Origin或绑定地址，不修改HTTP/HTTPS配置。自定义过项目名/Volume或安装目录的部署，应保留实际同一项目及Volume身份，不能另建空Volume冒充升级。确认没有queued/running任务后再重启，避免取消仍在等待的操作；已派发结果未知的任务继续禁止自动重放。保留旧镜像和备份用于必要回滚。
