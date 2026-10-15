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
assert record['version']==version and version=='v0.1.0-beta.4' and re.fullmatch(r'sha256:[0-9a-f]{64}',record['digest'])
assert record['image']=='ghcr.io/pixingzoudaiyuexing/narbe'
assert record['applicationReviewedSha']=='e22627ee181dac1d754a11d9a45ca5376f0d55a9'
assert re.fullmatch(r'[0-9a-f]{40}',record['releaseReviewedSha']) and re.fullmatch(r'[0-9a-f]{40}',record['sourceSha'])
assert re.fullmatch(r'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/[0-9]+',record['workflowUrl'])
manifests=record['manifest']['manifests']
assert len(manifests)==2 and {(m['platform']['os'],m['platform']['architecture']) for m in manifests}=={('linux','amd64'),('linux','arm64')}
assert all(re.fullmatch(r'sha256:[0-9a-f]{64}',m['digest']) for m in manifests)
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
