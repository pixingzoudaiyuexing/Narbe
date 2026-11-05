#!/usr/bin/env python3
import argparse
import hashlib
import io
import json
import re
from pathlib import Path
import tarfile

parser=argparse.ArgumentParser()
parser.add_argument('record',type=Path)
parser.add_argument('output',type=Path)
args=parser.parse_args()
record=json.loads(args.record.read_text())
version=Path('release/VERSION').read_text().strip()
OWNER_RELEASE_MODE='owner-authorized-manual-review-waiver'
OWNER_APPLICATION_BASELINE='3216f1e9e5151338427c7ceee5fe0090fdebf1e4'
OWNER_INTEGRATED_PR_HEAD='ec66fd4e90f38a1940b8cdf662868bd1a5a0a70e'
WORKFLOW_PATTERN=r'https://github\.com/pixingzoudaiyuexing/Narbe/actions/runs/[0-9]+'


def require(condition,message):
    if not condition:
        raise ValueError(message)


require(record['version']==version and version=='v0.1.0-beta.5' and re.fullmatch(r'sha256:[0-9a-f]{64}',record['digest']),'Invalid release identity or digest')
require(record['image']=='ghcr.io/pixingzoudaiyuexing/narbe','Invalid release identity or digest')


def validate_owner_release(r):
    # Owner waiver is an explicit authorization type, never an Independent
    # Reviewer approval. Legacy reviews can only be listed under historical.
    forbidden={'applicationReviewedSha','releaseReviewedSha','deliveryEvidenceSha','independentReview','manualIndependentReview'}
    require(not forbidden.intersection(r),'Owner waiver cannot claim independent review')
    source=r['sourceSha']
    require(isinstance(source,str) and re.fullmatch(r'[0-9a-f]{40}',source),'Invalid source SHA')
    require(r['authorizedSourceSha']==source,'Authorized source must equal the exact build source')
    require(r['applicationBaselineSha']==OWNER_APPLICATION_BASELINE,'Incorrect tested application baseline')
    require(r['integratedPRHead']==OWNER_INTEGRATED_PR_HEAD,'Incorrect integrated PR head')
    auth=r['ownerAuthorization']
    expected={'mode':OWNER_RELEASE_MODE,'authorizedBy':'Owner','authorizationDate':'2026-11-05','manualIndependentReview':'waived-by-owner','applicationBaselineSha':OWNER_APPLICATION_BASELINE,'integratedPRHead':OWNER_INTEGRATED_PR_HEAD}
    require(isinstance(auth,dict) and all(auth.get(k)==v for k,v in expected.items()),'Missing or false Owner authorization')
    require(not (forbidden-{'manualIndependentReview'}).intersection(auth),'Owner authorization cannot claim independent review')
    gate=r['sourceGate']
    require(isinstance(gate,dict) and not forbidden.intersection(gate),'Owner source gate cannot claim independent review')
    require(all(gate.get(k)==r[k] for k in ('releaseMode','sourceSha','authorizedSourceSha','applicationBaselineSha','integratedPRHead','ownerAuthorization')),'Source gate does not match release record')
    require('version' not in gate or gate['version']==r['version'],'Source gate version mismatch')
    ci=r['automatedValidation']
    require(isinstance(ci,dict) and ci.get('sourceSha')==source and ci.get('conclusion')=='success','CI must have passed for the exact source')
    ci_url=ci.get('workflowUrl')
    require(isinstance(ci_url,str) and re.fullmatch(WORKFLOW_PATTERN,ci_url),'Invalid CI workflow URL')
    jobs=ci.get('jobs')
    require(isinstance(jobs,list) and len(jobs)==2 and {j.get('name') for j in jobs if isinstance(j,dict)}=={'verify','arm64-runtime'},'Both required native CI jobs must be present exactly once')
    for job in jobs:
        minimum=24 if job['name']=='verify' else 12
        require(job.get('conclusion')=='success','Failed or skipped CI job')
        require(type(job.get('stepCount')) is int and job['stepCount']>=minimum,'Incomplete CI job steps')
        url=job.get('url')
        require(isinstance(url,str) and re.fullmatch(re.escape(ci_url)+r'/job/[0-9]+',url),'CI job URL does not belong to the validated run')


if record.get('releaseMode')==OWNER_RELEASE_MODE:
    validate_owner_release(record)
else:
    require('releaseMode' not in record,'Unknown release authorization mode')
    require(record['applicationReviewedSha']=='182e57759494c6fbcf6e3291d8259b524a9ea49a','Invalid release provenance, version, or platform')
    require(record['deliveryEvidenceSha']=='800c5443cb5fb913e30fe71467642c6fdfa87b00','Invalid release provenance, version, or platform')
    require(all(record['sourceGate'][key]==record[key] for key in ('version','sourceSha','applicationReviewedSha','releaseReviewedSha','deliveryEvidenceSha')),'Invalid release provenance, version, or platform')
    require(record['sourceGate']['applicationEvidenceChanges']==['docs/BETA5_DELIVERY.md','docs/BETA5_EVIDENCE.json','docs/BETA5_SOURCES.md'],'Invalid release provenance, version, or platform')
    require(set(record['sourceGate']['postReviewTextChanges']) <= {'release/README.md','release/NOTES.md'},'Invalid release provenance, version, or platform')
    require(re.fullmatch(r'[0-9a-f]{40}',record['releaseReviewedSha']) and re.fullmatch(r'[0-9a-f]{40}',record['sourceSha']),'Invalid release provenance, version, or platform')
require(re.fullmatch(WORKFLOW_PATTERN,record['workflowUrl']),'Invalid release provenance, version, or platform')
manifests=record['manifest']['manifests']
require(len(manifests)==2 and {(m['platform']['os'],m['platform']['architecture']) for m in manifests}=={('linux','amd64'),('linux','arm64')},'Invalid release provenance, version, or platform')
require(all(re.fullmatch(r'sha256:[0-9a-f]{64}',m['digest']) for m in manifests),'Invalid release provenance, version, or platform')
args.output.mkdir(parents=True,exist_ok=True)
files={name:Path('release',name).read_bytes() for name in ('compose.yaml','install.sh','.env.example','README.md','VERSION')}
files['image.env']=f"NARBE_IMAGE={record['image']}:{version}@{record['digest']}\n".encode()
files['release.json']=(json.dumps(record,ensure_ascii=False,indent=2)+'\n').encode()
archive=args.output/f'narbe-{version}-install.tar.gz'
with tarfile.open(archive,'w:gz') as tar:
    for name,data in files.items():
        info=tarfile.TarInfo(name);info.size=len(data);info.mode=0o755 if name=='install.sh' else 0o644;info.mtime=0
        tar.addfile(info,io.BytesIO(data))
(args.output/'SHA256SUMS').write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')
(args.output/'release-evidence.json').write_text(json.dumps(record,ensure_ascii=False,indent=2)+'\n')
print('Created:',archive.name)
