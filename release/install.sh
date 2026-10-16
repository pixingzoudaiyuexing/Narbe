#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
umask 077
command -v docker >/dev/null
docker info >/dev/null
docker compose version >/dev/null
if [ ! -f image.env ]; then echo '缺少发布镜像锁文件 image.env，请下载完整安装包。' >&2; exit 1; fi
if [ ! -f .env ]; then cp .env.example .env; fi
mkdir -p secrets
# Never follow an unexpected link or overwrite an existing password/configuration.
if [ -L secrets ] || [ -L secrets/admin-password ]; then echo '密码目录或文件不能是符号链接。' >&2; exit 1; fi
if [ ! -f secrets/admin-password ]; then
  printf '设置管理员密码（至少12字符，不会显示）: ' >&2
  IFS= read -r -s password
  printf '\n再次输入: ' >&2
  IFS= read -r -s confirmation
  printf '\n' >&2
  if [ "$password" != "$confirmation" ] || [ "${#password}" -lt 12 ] || [ "${#password}" -gt 256 ]; then echo '密码不一致或长度无效' >&2; exit 1; fi
  printf '%s' "$password" > secrets/admin-password
  unset password confirmation
fi
# Same secret handling as the reviewed developer installer: private parent,
# readable single-file mount for the non-root application user.
chmod 700 secrets
chmod 644 secrets/admin-password
docker compose --env-file .env --env-file image.env -f compose.yaml pull
docker compose --env-file .env --env-file image.env -f compose.yaml up -d --no-build --wait
echo 'Narbe 测试版已启动，默认访问 http://localhost:8080。远程请使用SSH隧道或可信HTTPS反向代理。'
echo '首次使用刚才设置的管理员密码登录。已有数据库时，启动不会覆盖数据库内的密码。'
