#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
umask 077

fail() { echo "$1" >&2; exit 1; }
regular() { [ -f "$1" ] && [ ! -L "$1" ]; }
mode=${1:-up}
case "$mode" in up|restart|stop|status|logs) ;; *) fail '用法：./install-probe.sh [up|restart|stop|status|logs]' ;; esac
[ "$#" -le 1 ] || fail '仅接受一个操作参数。'
regular probe-image.env || fail '缺少 probe-image.env，或镜像锁文件不是普通文件；请下载完整正式安装包。'
regular probe-compose.yaml || fail '缺少普通文件 probe-compose.yaml。'
# Do not source an environment file as shell code. Only the exact released
# package/tag and an immutable digest are accepted; no latest/local build.
image_lock=$(cat probe-image.env)
[[ "$image_lock" =~ ^NARBE_PROBE_IMAGE=ghcr\.io/pixingzoudaiyuexing/narbe:v0\.1\.0-beta\.6-probe@sha256:[0-9a-f]{64}$ ]] || fail '探针镜像锁无效；必须使用 beta.6 正式版本和 SHA-256 Digest。'

if [ ! -e probe.env ] && [ ! -L probe.env ]; then
  [ "$mode" = up ] || fail '尚未安装探针，请先运行 ./install-probe.sh。'
  printf 'Narbe 管理端 HTTPS 地址（例如 https://narbe.example.com）: ' >&2
  IFS= read -r control_url || fail '未读取到 HTTPS 地址。'
  control_url=${control_url%/}
  [[ "$control_url" =~ ^https://([A-Za-z0-9][A-Za-z0-9.-]*|\[[0-9A-Fa-f:]+\])(:[0-9]{1,5})?$ ]] || fail '请输入 HTTPS Origin，不能包含路径、账号、查询或片段。'
  if [[ "$control_url" =~ :([0-9]+)$ ]]; then
    port=$((10#${BASH_REMATCH[1]}))
    [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail 'HTTPS 端口无效。'
  fi
  printf 'NARBE_PROBE_CONTROL_URL=%s\nNARBE_PROBE_ALLOWED_PORTS=22,80,443,8443,8080\nNARBE_PROBE_NETWORK_MODE=bridge\nNARBE_PROBE_PROJECT_NAME=narbe-probe\n' "$control_url" > probe.env
fi
regular probe.env || fail 'probe.env 必须是普通文件，不能使用符号链接。'
control_url= allowed_ports= network_mode= project_name=
count=0
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    NARBE_PROBE_CONTROL_URL=*) [ -z "$control_url" ] || fail 'HTTPS 地址配置重复。'; control_url=${line#*=} ;;
    NARBE_PROBE_ALLOWED_PORTS=*) [ -z "$allowed_ports" ] || fail '端口配置重复。'; allowed_ports=${line#*=} ;;
    NARBE_PROBE_NETWORK_MODE=*) [ -z "$network_mode" ] || fail '网络配置重复。'; network_mode=${line#*=} ;;
    NARBE_PROBE_PROJECT_NAME=*) [ -z "$project_name" ] || fail '项目名配置重复。'; project_name=${line#*=} ;;
    *) fail 'probe.env 含未知或无效配置；请保留安装器生成的四项配置。' ;;
  esac
  count=$((count + 1))
done < probe.env
[ "$count" = 4 ] || fail 'probe.env 配置不完整。'
[[ "$control_url" =~ ^https://([A-Za-z0-9][A-Za-z0-9.-]*|\[[0-9A-Fa-f:]+\])(:[0-9]{1,5})?$ ]] || fail 'probe.env 中必须是可信 HTTPS Origin。'
if [[ "$control_url" =~ :([0-9]+)$ ]]; then
  port=$((10#${BASH_REMATCH[1]}))
  [ "$port" -ge 1 ] && [ "$port" -le 65535 ] || fail 'HTTPS 端口无效。'
fi
[[ "$allowed_ports" =~ ^[0-9]{1,5}(,[0-9]{1,5}){0,31}$ ]] || fail '探针允许端口格式无效（最多32项）。'
IFS=, read -r -a ports <<< "$allowed_ports"
for port in "${ports[@]}"; do
  number=$((10#$port))
  [ "$number" -ge 1 ] && [ "$number" -le 65535 ] || fail '探针允许端口范围无效。'
done
case "$network_mode" in bridge|host) ;; *) fail '网络模式仅支持 bridge 或 host。' ;; esac
[[ "$project_name" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ ]] || fail '探针 Compose 项目名无效。'
[ ! -L secrets ] || fail 'secrets 不能是符号链接。'
if [ ! -d secrets ]; then
  [ "$mode" = up ] || fail '缺少探针注册凭据目录。'
  mkdir secrets
fi
[ ! -L secrets/probe-registration-token ] || fail '探针注册凭据不能是符号链接。'
if [ ! -e secrets/probe-registration-token ]; then
  [ "$mode" = up ] || fail '缺少探针注册凭据。'
  printf '一次性探针注册凭据（不会显示；不要输入云厂商 Token）: ' >&2
  IFS= read -r -s registration_token || fail '未读取到注册凭据。'
  printf '\n' >&2
  [[ "$registration_token" =~ ^[A-Za-z0-9_-]+$ ]] && [ "${#registration_token}" -ge 32 ] && [ "${#registration_token}" -le 256 ] || fail '注册凭据格式无效；请在管理端重新生成。'
  printf '%s' "$registration_token" > secrets/probe-registration-token
  unset registration_token
fi
regular secrets/probe-registration-token || fail '探针注册凭据必须是普通文件。'
# Parent directory prevents other host users from traversing. The single-file
# read-only secret mount is readable by the unprivileged container UID 65532.
chmod 700 secrets
chmod 644 secrets/probe-registration-token
chmod 600 probe.env
command -v docker >/dev/null || fail '请先安装 Docker Engine 和 Docker Compose。'
docker info >/dev/null
docker compose version >/dev/null
# Compose gives shell variables priority over --env-file. Export only the
# values validated above so a stale development shell cannot override the
# pinned image, controller, allowed ports or network mode.
export NARBE_PROBE_IMAGE="${image_lock#*=}"
export NARBE_PROBE_CONTROL_URL="$control_url"
export NARBE_PROBE_ALLOWED_PORTS="$allowed_ports"
export NARBE_PROBE_NETWORK_MODE="$network_mode"
compose=(docker compose --project-name "$project_name" --env-file probe.env --env-file probe-image.env -f probe-compose.yaml)
case "$mode" in
  up)
    "${compose[@]}" pull
    "${compose[@]}" up -d --no-build --wait --wait-timeout 180
    echo 'Narbe Probe 已启动：主动 HTTPS 连接管理端，无入站端口，不持有云账号密钥。'
    echo '在线状态及实际出口以管理端为准；Clash/TUN/VPN 与 Docker 网络可能改变出口。'
    echo '再次运行保留 probe.env、探针身份和数据 Volume，不会覆盖已有凭据。'
    ;;
  restart) "${compose[@]}" restart ;;
  stop) "${compose[@]}" stop ;;
  status) "${compose[@]}" ps ;;
  logs) "${compose[@]}" logs --tail=50 probe ;;
esac
