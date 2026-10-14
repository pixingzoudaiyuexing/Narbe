#!/usr/bin/env bash
set -euo pipefail
version=$(cat release/VERSION)
image=ghcr.io/pixingzoudaiyuexing/narbe
[[ "$version" = v0.1.0-beta.2 ]]
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$REVIEWED_SHA" =~ ^[0-9a-f]{40}$ ]]
umask 077
export DOCKER_CONFIG="$RUNNER_TEMP/narbe-publish-auth"
mkdir -p "$DOCKER_CONFIG"
trap 'rm -rf "$DOCKER_CONFIG"' EXIT
printf '%s' "$GH_TOKEN" | docker login ghcr.io -u "$GITHUB_ACTOR" --password-stdin
# No final version tag is touched before both native build/runtime jobs succeed.
python3 release/registry.py "$version" --absent
for arch in amd64 arm64; do
  archive=".local/release-input/narbe-image-$arch/narbe-$arch.tar.gz"
  gzip -dc "$archive" | docker load
  local_image="narbe/release:$GITHUB_SHA-$arch"
  [[ "$(docker image inspect "$local_image" --format '{{.Architecture}}')" = "$arch" ]]
  [[ "$(docker image inspect "$local_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$GITHUB_SHA" ]]
  [[ "$(docker image inspect "$local_image" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" = "$version" ]]
  python3 release/registry.py "$version-$arch" > "$RUNNER_TEMP/variant-$arch.json" || exit 1
  if [ "$(cat "$RUNNER_TEMP/variant-$arch.json")" = null ]; then
    docker tag "$local_image" "$image:$version-$arch"
    docker push "$image:$version-$arch"
  else
    # A partial same-commit retry may reuse a variant; never overwrite its tag.
    docker pull --platform "linux/$arch" "$image:$version-$arch"
    [[ "$(docker image inspect "$image:$version-$arch" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$GITHUB_SHA" ]]
    [[ "$(docker image inspect "$image:$version-$arch" --format '{{.Architecture}}')" = "$arch" ]]
    [[ "$(docker image inspect "$image:$version-$arch" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" = "$version" ]]
  fi
done
python3 release/registry.py "$version" --absent
docker buildx imagetools create -t "$image:$version" "$image:$version-amd64" "$image:$version-arm64"
mkdir -p .local/release-output
python3 release/registry.py "$version" > .local/release-output/registry.json
python3 - <<'PY'
import json,os
from pathlib import Path
record=json.loads(Path('.local/release-output/registry.json').read_text())
platforms={(m['platform']['os'],m['platform']['architecture']) for m in record['manifest']['manifests']}
assert platforms=={('linux','amd64'),('linux','arm64')},platforms
record.update(version=Path('release/VERSION').read_text().strip(),image='ghcr.io/pixingzoudaiyuexing/narbe',sourceSha=os.environ['GITHUB_SHA'],reviewedSha=os.environ['REVIEWED_SHA'],workflowUrl='https://github.com/'+os.environ['GITHUB_REPOSITORY']+'/actions/runs/'+os.environ['GITHUB_RUN_ID'])
Path('.local/release-output/release.json').write_text(json.dumps(record,ensure_ascii=False,indent=2)+'\n')
print('Published immutable index digest:',record['digest'])
PY
