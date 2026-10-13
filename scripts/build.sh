#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
command -v docker >/dev/null
docker info >/dev/null
available_kb=$(df -Pk . | awk 'NR==2 {print $4}')
if [ "$available_kb" -lt 2097152 ]; then
  echo '至少需要 2 GiB 可用空间；不会清理其他项目。' >&2
  exit 1
fi
mkdir -p .local
if [ -f .local/cleanup-pending ]; then echo "上次 Narbe 清理尚未核实，请先处理 .local/cleanup-pending；停止新增构建。" >&2; exit 1; fi
mkdir .local/build.lock
build_dir=$(mktemp -d .local/build-XXXXXX)
run_id="$(date +%s)-$$"
builder="narbe-build-$run_id"
container="narbe-smoke-$run_id"
candidate="narbe/local:candidate-$run_id"
old_id=""
builder_attempted=0
cleanup() {
  status=$?
  cleanup_failed=0
  trap - EXIT
  # Match our unique run label, including a container created before docker run failed.
  own_containers=$(docker ps -aq --filter "label=io.narbe.validation.run=$run_id") || { status=1; cleanup_failed=1; }
  for own_id in $own_containers; do
    if ! docker rm -f "$own_id" >/dev/null; then echo "本次验证容器清理失败：$own_id" >&2; status=1; cleanup_failed=1; fi
  done
  if [ "$builder_attempted" = 1 ]; then
    if ! docker buildx rm "$builder" >/dev/null; then echo "本次专用 Builder 清理失败：$builder" >&2; status=1; cleanup_failed=1; fi
if docker buildx inspect "$builder" >/dev/null 2>&1; then echo "Builder 仍存在：$builder" >&2; status=1; cleanup_failed=1; fi
  fi
  candidate_ids=$(docker image ls -q --no-trunc "$candidate") || { status=1; cleanup_failed=1; }
  if [ -n "$candidate_ids" ]; then
    refs=$(docker ps -aq --filter "ancestor=$candidate") || { status=1; cleanup_failed=1; }
    if [ -z "$refs" ]; then
      if ! docker image rm "$candidate" >/dev/null; then echo "本次候选标签清理失败：$candidate" >&2; status=1; cleanup_failed=1; fi
    else
      echo "本次候选仍被容器引用，保留：$candidate" >&2
      status=1;cleanup_failed=1
    fi
  fi
  rm -rf "$build_dir"
  rmdir .local/build.lock || { status=1; cleanup_failed=1; }
  if [ "$cleanup_failed" != 0 ]; then printf "%s\n" "$run_id" > .local/cleanup-pending; fi
  df -h .
  exit "$status"
}
trap cleanup EXIT
old_ids=$(docker image ls -q --no-trunc narbe/local:current)
if [ -n "$old_ids" ]; then old_id=$(docker image inspect narbe/local:current --format '{{.Id}}'); fi
if docker buildx inspect "$builder" >/dev/null 2>&1; then echo '专用 Builder 名称冲突，停止构建' >&2; exit 1; fi
builder_attempted=1
if [ -n "${NARBE_BUILDKIT_IMAGE:-}" ]; then
  docker buildx create --name "$builder" --driver docker-container --driver-opt "image=$NARBE_BUILDKIT_IMAGE" >/dev/null
else
  docker buildx create --name "$builder" --driver docker-container >/dev/null
fi
docker buildx build --builder "$builder" --load --build-arg "GOPROXY=${NARBE_GOPROXY:-https://proxy.golang.org,direct}" -t "$candidate" .
openssl rand -base64 36 > "$build_dir/password"
chmod 644 "$build_dir/password"
docker run -d --name "$container" --label io.narbe.project=Narbe --label io.narbe.disposable=true --label "io.narbe.validation.run=$run_id" \
  --read-only --cap-drop ALL --security-opt no-new-privileges:true --pids-limit 128 --memory 384m \
  --log-driver json-file --log-opt max-size=1m --log-opt max-file=2 \
  --tmpfs /data:uid=65532,gid=65532,mode=0700,size=32m \
  --mount "type=bind,src=$(pwd)/$build_dir/password,dst=/run/password,readonly" \
  -e NARBE_ADMIN_PASSWORD_FILE=/run/password "$candidate" >/dev/null
healthy=0
for attempt in $(seq 1 30); do
  if docker exec "$container" /narbe healthcheck; then healthy=1; break; fi
  sleep 1
done
if [ "$healthy" != 1 ]; then echo '新镜像健康检查失败；旧镜像保留，本次失败候选将回收。' >&2; exit 1; fi
docker tag "$candidate" narbe/local:current
new_id=$(docker image inspect "$candidate" --format '{{.Id}}')
# Persist exact prior IDs so compose replacement can collect them after references disappear.
if [ -n "$old_id" ] && [ "$old_id" != "$new_id" ]; then
  if ! ./scripts/collect-images.sh "$old_id"; then printf "collection prior=%s\n" "$old_id" > .local/cleanup-pending; exit 1; fi
else
  if ! ./scripts/collect-images.sh; then echo collection > .local/cleanup-pending; exit 1; fi
fi

docker image inspect narbe/local:current --format 'Validated image {{.Id}}, bytes={{.Size}}'
