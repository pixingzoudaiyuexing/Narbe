#!/usr/bin/env python3
"""Package a verified beta.6 manager and Probe release without false review claims."""
import argparse
import hashlib
import io
import json
import re
from pathlib import Path
import tarfile

VERSION='v0.1.0-beta.6'
MODE='owner-authorized-test-release'
BASELINE='8d36215df29d2c1bb854c61afcc00d4582c7fc8f'
IMAGE='ghcr.io/pixingzoudaiyuexing/narbe'
WORKFLOW_PATTERN=r'https://github\.com/pixingzoudaiyuexing/Narbe/actions/runs/[0-9]+'
FORBIDDEN={'applicationReviewedSha','releaseReviewedSha','deliveryEvidenceSha','independentReview'}

def require(condition,message):
    if not condition:raise ValueError(message)

def manifest(record):
    require(isinstance(record,dict) and isinstance(record.get('digest'),str) and re.fullmatch(r'sha256:[0-9a-f]{64}',record['digest']),'Invalid image index digest')
    manifests=record.get('manifest',{}).get('manifests')
    require(isinstance(manifests,list) and len(manifests)==2,'Both native architecture manifests are required')
    require({(m.get('platform',{}).get('os'),m.get('platform',{}).get('architecture')) for m in manifests}=={('linux','amd64'),('linux','arm64')},'Invalid image platforms')
    require(all(isinstance(m.get('digest'),str) and re.fullmatch(r'sha256:[0-9a-f]{64}',m['digest']) for m in manifests),'Invalid architecture digest')

def validate(record):
    require(not FORBIDDEN.intersection(record),'Owner test release cannot claim a new independent review')
    source=record.get('sourceSha')
    require(isinstance(source,str) and re.fullmatch(r'[0-9a-f]{40}',source),'Invalid source SHA')
    require(record.get('authorizedSourceSha')==source and record.get('applicationBaselineSha')==BASELINE,'Wrong authorized source or tested application baseline')
    require(record.get('releaseMode')==MODE and record.get('version')==VERSION and record.get('image')==IMAGE,'Invalid release identity or authorization mode')
    auth=record.get('ownerAuthorization',{})
    expected={'mode':MODE,'authorizedBy':'Owner','authorizationDate':'2026-11-20','authorizationText':'发布新的测试版本','manualIndependentReview':'not-performed-for-this-candidate','applicationBaselineSha':BASELINE,'version':VERSION}
    require(isinstance(auth,dict) and all(auth.get(k)==v for k,v in expected.items()) and not FORBIDDEN.intersection(auth),'Missing or false Owner authorization')
    gate=record.get('sourceGate',{})
    require(isinstance(gate,dict) and not FORBIDDEN.intersection(gate),'Source gate cannot claim independent review')
    require(all(gate.get(k)==record[k] for k in ('releaseMode','sourceSha','authorizedSourceSha','applicationBaselineSha','ownerAuthorization','version')),'Source gate does not match release')
    workflow=record.get('workflowUrl')
    require(isinstance(workflow,str) and re.fullmatch(WORKFLOW_PATTERN,workflow),'Invalid release workflow URL')
    ci=record.get('automatedValidation',{})
    require(isinstance(ci,dict) and ci.get('sourceSha')==source and ci.get('conclusion')=='success' and ci.get('workflowPath')=='.github/workflows/ci.yml','CI must pass for this exact source')
    ci_url=ci.get('workflowUrl')
    require(isinstance(ci_url,str) and re.fullmatch(WORKFLOW_PATTERN,ci_url),'Invalid CI workflow URL')
    jobs=ci.get('jobs')
    require(isinstance(jobs,list) and len(jobs)==2 and {j.get('name') for j in jobs if isinstance(j,dict)}=={'verify','arm64-runtime'},'Both successful native CI jobs are required exactly once')
    for job in jobs:
        minimum=24 if job['name']=='verify' else 12
        require(job.get('conclusion')=='success' and type(job.get('stepCount')) is int and job['stepCount']>=minimum,'Incomplete or skipped CI job')
        require(isinstance(job.get('url'),str) and re.fullmatch(re.escape(ci_url)+r'/job/[0-9]+',job['url']),'CI job does not belong to validated run')
    manifest(record)
    probe=record.get('probe')
    require(isinstance(probe,dict) and probe.get('image')==IMAGE and probe.get('tag')==VERSION+'-probe' and probe.get('sourceSha')==source,'Missing or incorrect Probe release identity')
    require(not FORBIDDEN.intersection(probe),'Probe cannot claim a new independent review')
    manifest(probe)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('record',type=Path);parser.add_argument('output',type=Path);args=parser.parse_args()
    record=json.loads(args.record.read_text());validate(record)
    require(Path('release/VERSION').read_text().strip()==VERSION,'Version file differs from release record')
    files={name:Path('release',name).read_bytes() for name in ('compose.yaml','install.sh','.env.example','README.md','VERSION','probe-compose.yaml','install-probe.sh')}
    files['image.env']=f"NARBE_IMAGE={IMAGE}:{VERSION}@{record['digest']}\n".encode()
    files['probe-image.env']=f"NARBE_PROBE_IMAGE={IMAGE}:{VERSION}-probe@{record['probe']['digest']}\n".encode()
    files['release.json']=(json.dumps(record,ensure_ascii=False,indent=2)+'\n').encode()
    args.output.mkdir(parents=True,exist_ok=False)
    archive=args.output/f'narbe-{VERSION}-install.tar.gz'
    with tarfile.open(archive,'w:gz') as tar:
        for name,data in files.items():
            info=tarfile.TarInfo(name);info.size=len(data);info.mode=0o755 if name.endswith('.sh') else 0o644;info.mtime=0
            tar.addfile(info,io.BytesIO(data))
    (args.output/'release-evidence.json').write_text(json.dumps(record,ensure_ascii=False,indent=2)+'\n')
    names=[archive.name,'release-evidence.json']
    (args.output/'SHA256SUMS').write_text(''.join(hashlib.sha256((args.output/name).read_bytes()).hexdigest()+'  '+name+'\n' for name in names))
    print('Created:',archive.name)

if __name__=='__main__':main()
