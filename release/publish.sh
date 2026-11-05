#!/usr/bin/env bash
set -euo pipefail
version=$(cat release/VERSION)
image=ghcr.io/pixingzoudaiyuexing/narbe
[[ "$version" = v0.1.0-beta.5 ]]
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$AUTHORIZED_SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$AUTHORIZED_SOURCE_SHA" = "$GITHUB_SHA" ]]
# Extracted from the exact Owner-authorized commit; this is not an independent review.
python3 "$RUNNER_TEMP/narbe-authorized-source.py" --owner-authorized-source "$AUTHORIZED_SOURCE_SHA" --head "$GITHUB_SHA" --output "$RUNNER_TEMP/narbe-source-gate.json"
# The same workflow's read-only verify-ci Job validated every job/step before
# this job obtained package permissions. Refuse mismatched downloaded evidence.
python3 - <<'CI'
import json,os,re
from pathlib import Path
record=json.loads(Path('.local/release-ci/ci.json').read_text())
if not (record['sourceSha']==os.environ['GITHUB_SHA'] and record['conclusion']=='success'): raise SystemExit('CI evidence source or result mismatch')
if record['workflowPath']!='.github/workflows/ci.yml': raise SystemExit('Wrong validation workflow')
if not re.fullmatch(r'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/[0-9]+',record['workflowUrl']): raise SystemExit('Invalid CI provenance')
if not ({j['name'] for j in record['jobs']}=={'verify','arm64-runtime'} and len(record['jobs'])==2): raise SystemExit('Missing native CI jobs')
if not all(j['conclusion']=='success' and j['stepCount']>0 for j in record['jobs']): raise SystemExit('Incomplete CI jobs')
CI
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
if platforms!={('linux','amd64'),('linux','arm64')}: raise SystemExit('Published index has incorrect platforms')
source_gate=json.loads(Path(os.environ['RUNNER_TEMP'],'narbe-source-gate.json').read_text())
validation=json.loads(Path('.local/release-ci/ci.json').read_text())
record.update(releaseMode=source_gate['releaseMode'],authorizedSourceSha=source_gate['authorizedSourceSha'],applicationBaselineSha=source_gate['applicationBaselineSha'],integratedPRHead=source_gate['integratedPRHead'],ownerAuthorization=source_gate['ownerAuthorization'],automatedValidation=validation,sourceGate=source_gate,version=Path('release/VERSION').read_text().strip(),image='ghcr.io/pixingzoudaiyuexing/narbe',sourceSha=os.environ['GITHUB_SHA'],workflowUrl='https://github.com/'+os.environ['GITHUB_REPOSITORY']+'/actions/runs/'+os.environ['GITHUB_RUN_ID'])
Path('.local/release-output/release.json').write_text(json.dumps(record,ensure_ascii=False,indent=2)+'\n')
print('Published immutable index digest:',record['digest'])
PY
