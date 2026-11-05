#!/usr/bin/env bash
# Collect only IDs explicitly recorded by build.sh; protect every remaining tag/reference.
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .local
if ! mkdir .local/collect-images.lock; then echo collector-lock > .local/cleanup-pending; exit 1; fi
ledger=.local/managed-image-ids
next=$(mktemp .local/managed-images-XXXXXX)
cleanup() { status=$?; trap - EXIT; rm -f "$next"; if ! rmdir .local/collect-images.lock; then status=1; fi; if [ "$status" != 0 ]; then echo collector > .local/cleanup-pending; fi; exit "$status"; }
trap cleanup EXIT
if [ -n "${1:-}" ]; then printf '%s\n' "$1" >> "$ledger"; fi
if [ ! -f "$ledger" ]; then exit 0; fi
# A successful inventory query can distinguish absent images from daemon/query failures.
all_ids=$(docker image ls -a --no-trunc --format '{{.ID}}')
while IFS= read -r image_id; do
  [ -z "$image_id" ] && continue
  if ! printf '%s\n' "$all_ids" | sed 's/^sha256://' | awk -v wanted="${image_id#sha256:}" '$0==wanted{found=1} END{exit !found}'; then continue; fi
  owner=$(docker image inspect "$image_id" --format '{{index .Config.Labels "io.narbe.project"}}')
  if [ "$owner" != Narbe ]; then printf '%s\n' "$image_id" >> "$next"; continue; fi
  refs=$(docker ps -aq --filter "ancestor=$image_id")
  tags=$(docker image inspect "$image_id" --format '{{range .RepoTags}}{{println .}}{{end}}')
  if [ -z "$refs" ] && [ -z "$tags" ]; then
    docker image rm "$image_id"
  else
    printf '%s\n' "$image_id" >> "$next"
  fi
done < <(sort -u "$ledger")
mv "$next" "$ledger"
