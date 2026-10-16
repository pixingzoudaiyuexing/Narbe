#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
umask 077
mkdir -p .local
if [ ! -f .env ]; then cp .env.example .env; fi
# Compose resolves .env and environment consistently; never execute .env as shell code.
password_file=$(docker compose config --format json | python3 -c 'import json,sys;print(json.load(sys.stdin)["secrets"]["admin_password"]["file"])')
default_file="$(pwd)/.local/admin-password"
if [ "$password_file" != "$default_file" ]; then
  if [ ! -f "$password_file" ]; then echo '自定义密码文件不存在，请先建立该文件。不会创建或覆盖外部文件。' >&2; exit 1; fi
else
  if [ ! -f "$password_file" ]; then
    printf '设置管理员密码（至少12字符，不会显示）: ' >&2
    IFS= read -r -s password
    printf '\n再次输入: ' >&2
    IFS= read -r -s confirmation
    printf '\n' >&2
    if [ "$password" != "$confirmation" ] || [ "${#password}" -lt 12 ]; then echo '密码不一致或过短' >&2; exit 1; fi
    printf '%s' "$password" > "$password_file"
    unset password confirmation
  fi
  chmod 700 .local
  # Parent stays private; single file bind mount must be readable by container UID 65532.
  chmod 644 "$password_file"
fi
./scripts/build.sh
docker compose up -d --no-build --wait
./scripts/collect-images.sh
echo 'Narbe 已启动。默认访问 http://localhost:8080；自定义地址请查看 .env。'
