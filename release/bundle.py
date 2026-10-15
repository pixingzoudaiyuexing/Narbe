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
assert record['version']==version and version=='v0.1.0-beta.3' and re.fullmatch(r'sha256:[0-9a-f]{64}',record['digest'])
assert record['image']=='ghcr.io/pixingzoudaiyuexing/narbe'
assert record['applicationReviewedSha']=='4f1c9cc2a7c8e099ab3c375548c48e691d87c72d'
assert re.fullmatch(r'[0-9a-f]{40}',record['releaseReviewedSha']) and re.fullmatch(r'[0-9a-f]{40}',record['sourceSha'])
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
