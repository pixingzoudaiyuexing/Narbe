#!/usr/bin/env bash
set -euo pipefail
version=$(cat release/VERSION)
image=ghcr.io/pixingzoudaiyuexing/narbe
[[ "$version" = v0.1.0-beta.6 ]]
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$AUTHORIZED_SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$AUTHORIZED_SOURCE_SHA" = "$GITHUB_SHA" ]]
# Extracted from the exact Owner-authorized commit. No new formal review is claimed.
python3 "$RUNNER_TEMP/narbe-authorized-source.py" --owner-authorized-test-source "$AUTHORIZED_SOURCE_SHA" --head "$GITHUB_SHA" --output "$RUNNER_TEMP/narbe-source-gate.json"
python3 - <<'CI'
import json,os,re
from pathlib import Path
record=json.loads(Path('.local/release-ci/ci.json').read_text())
if not (record['sourceSha']==os.environ['GITHUB_SHA'] and record['conclusion']=='success'):raise SystemExit('CI evidence source or result mismatch')
if record['workflowPath']!='.github/workflows/ci.yml':raise SystemExit('Wrong validation workflow')
if not re.fullmatch(r'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/[0-9]+',record['workflowUrl']):raise SystemExit('Invalid CI provenance')
if not ({j['name'] for j in record['jobs']}=={'verify','arm64-runtime'} and len(record['jobs'])==2):raise SystemExit('Missing native CI jobs')
if not all(j['conclusion']=='success' and type(j['stepCount']) is int and j['stepCount']>0 for j in record['jobs']):raise SystemExit('Incomplete CI jobs')
CI
umask 077
export DOCKER_CONFIG="$RUNNER_TEMP/narbe-publish-auth"
mkdir -p "$DOCKER_CONFIG"
trap 'rm -rf "$DOCKER_CONFIG"' EXIT
printf '%s' "$GH_TOKEN" | docker login ghcr.io -u "$GITHUB_ACTOR" --password-stdin
# All variants are verified before either final index is created. A partial
# same-source retry can reuse content, but never replace an existing tag.
mkdir -p .local/release-output
for role in manager probe; do
  tag="$version"
  local_repo=narbe/release
  if [[ "$role" = probe ]]; then tag="$version-probe"; local_repo=narbe-probe/release; fi
  for arch in amd64 arm64; do
    archive=".local/release-input/narbe-images-$arch/$role-$arch.tar.gz"
    gzip -dc "$archive" | docker load
    local_image="$local_repo:$GITHUB_SHA-$arch"
    [[ "$(docker image inspect "$local_image" --format '{{.Architecture}}')" = "$arch" ]]
    [[ "$(docker image inspect "$local_image" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$GITHUB_SHA" ]]
    [[ "$(docker image inspect "$local_image" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" = "$version" ]]
    expected_title=Narbe
    if [[ "$role" = probe ]]; then expected_title='Narbe Probe'; fi
    [[ "$(docker image inspect "$local_image" --format '{{index .Config.Labels "org.opencontainers.image.title"}}')" = "$expected_title" ]]
    docker image inspect "$local_image" > "$RUNNER_TEMP/$role-$arch-built.json"
    python3 release/registry.py "$tag-$arch" > "$RUNNER_TEMP/$role-$arch.json"
    if [[ "$(cat "$RUNNER_TEMP/$role-$arch.json")" = null ]]; then
      docker tag "$local_image" "$image:$tag-$arch"
      docker push "$image:$tag-$arch"
    else
      # A retry may reuse an exact same-source variant, never overwrite it.
      docker pull --platform "linux/$arch" "$image:$tag-$arch"
      [[ "$(docker image inspect "$image:$tag-$arch" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}')" = "$GITHUB_SHA" ]]
      [[ "$(docker image inspect "$image:$tag-$arch" --format '{{.Architecture}}')" = "$arch" ]]
      [[ "$(docker image inspect "$image:$tag-$arch" --format '{{index .Config.Labels "org.opencontainers.image.version"}}')" = "$version" ]]
      [[ "$(docker image inspect "$image:$tag-$arch" --format '{{index .Config.Labels "org.opencontainers.image.title"}}')" = "$expected_title" ]]
      docker image inspect "$image:$tag-$arch" > "$RUNNER_TEMP/$role-$arch-existing.json"
      python3 release/verify_images.py image "$RUNNER_TEMP/$role-$arch-built.json" "$RUNNER_TEMP/$role-$arch-existing.json"
    fi
    python3 release/registry.py "$tag-$arch" > "$RUNNER_TEMP/$role-$arch.json"
  done
done
for role in manager probe; do
  tag="$version"
  if [[ "$role" = probe ]]; then tag="$version-probe"; fi
  python3 release/registry.py "$tag" > "$RUNNER_TEMP/$role-index.json"
  decision=$(python3 release/verify_images.py index "$RUNNER_TEMP/$role-index.json" "$RUNNER_TEMP/$role-amd64.json" "$RUNNER_TEMP/$role-arm64.json")
  if [[ "$decision" = absent ]]; then
    docker buildx imagetools create -t "$image:$tag" "$image:$tag-amd64" "$image:$tag-arm64"
  fi
  python3 release/registry.py "$tag" > ".local/release-output/$role-registry.json"
  python3 release/verify_images.py index ".local/release-output/$role-registry.json" "$RUNNER_TEMP/$role-amd64.json" "$RUNNER_TEMP/$role-arm64.json" >/dev/null
done
python3 - <<'PY'
import json,os
from pathlib import Path
records={role:json.loads(Path('.local/release-output',role+'-registry.json').read_text()) for role in ('manager','probe')}
for role,r in records.items():
    platforms={(m['platform']['os'],m['platform']['architecture']) for m in r['manifest']['manifests']}
    if len(r['manifest']['manifests'])!=2 or platforms!={('linux','amd64'),('linux','arm64')}:raise SystemExit('Published '+role+' index has incorrect platforms')
source_gate=json.loads(Path(os.environ['RUNNER_TEMP'],'narbe-source-gate.json').read_text())
validation=json.loads(Path('.local/release-ci/ci.json').read_text())
version=Path('release/VERSION').read_text().strip()
record=records['manager']
probe=records['probe'];probe.update(image='ghcr.io/pixingzoudaiyuexing/narbe',tag=version+'-probe',sourceSha=os.environ['GITHUB_SHA'])
record.update(releaseMode=source_gate['releaseMode'],authorizedSourceSha=source_gate['authorizedSourceSha'],applicationBaselineSha=source_gate['applicationBaselineSha'],ownerAuthorization=source_gate['ownerAuthorization'],automatedValidation=validation,sourceGate=source_gate,version=version,image='ghcr.io/pixingzoudaiyuexing/narbe',sourceSha=os.environ['GITHUB_SHA'],probe=probe,workflowUrl='https://github.com/'+os.environ['GITHUB_REPOSITORY']+'/actions/runs/'+os.environ['GITHUB_RUN_ID'])
Path('.local/release-output/release.json').write_text(json.dumps(record,ensure_ascii=False,indent=2)+'\n')
print('Published immutable manager index:',record['digest'])
print('Published immutable probe index:',probe['digest'])
PY
