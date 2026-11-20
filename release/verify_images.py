#!/usr/bin/env python3
"""Bind registry retries to the exact native-tested image content."""
import argparse
import json
import re
from pathlib import Path

DIGEST=re.compile(r'sha256:[0-9a-f]{64}')

def canonical_config(value):
    return {k:v for k,v in value.items() if not(v is None or v is False or v=='' or v==[] or v=={})}

def image_identity(info):
    if isinstance(info,list):
        if len(info)!=1:raise ValueError('Expected exactly one image inspect')
        info=info[0]
    if not isinstance(info,dict) or info.get('Os')!='linux' or info.get('Architecture') not in {'amd64','arm64'}:raise ValueError('Invalid native image identity')
    config=info.get('Config');root=info.get('RootFS',{})
    if not isinstance(config,dict) or root.get('Type')!='layers' or not isinstance(root.get('Layers'),list) or not root['Layers'] or not all(isinstance(d,str) and DIGEST.fullmatch(d) for d in root['Layers']):raise ValueError('Incomplete image config or ordered RootFS')
    return {'os':info['Os'],'architecture':info['Architecture'],'config':canonical_config(config),'layers':root['Layers']}

def verify_same_image(reference,actual):
    if image_identity(reference)!=image_identity(actual):raise ValueError('Existing immutable variant differs from native-tested content')

def variant_digest(value):
    if not isinstance(value,dict) or not isinstance(value.get('digest'),str) or not DIGEST.fullmatch(value['digest']):raise ValueError('Missing variant digest')
    m=value.get('manifest',{})
    if not isinstance(m,dict) or 'manifests' in m or m.get('schemaVersion')!=2 or not isinstance(m.get('config'),dict) or not DIGEST.fullmatch(m['config'].get('digest','')) or not isinstance(m.get('layers'),list) or not m['layers']:raise ValueError('Variant must be a single image manifest')
    return value['digest']

def verify_index(index,amd64,arm64):
    expected={('linux','amd64'):variant_digest(amd64),('linux','arm64'):variant_digest(arm64)}
    if index is None:return 'absent'
    if not isinstance(index,dict) or not isinstance(index.get('digest'),str) or not DIGEST.fullmatch(index['digest']):raise ValueError('Invalid existing index digest')
    rows=index.get('manifest',{}).get('manifests')
    if not isinstance(rows,list) or len(rows)!=2:raise ValueError('Index must contain exactly two native manifests')
    actual={}
    for row in rows:
        p=row.get('platform',{});key=(p.get('os'),p.get('architecture'))
        if key in actual:raise ValueError('Duplicate index platform')
        actual[key]=row.get('digest')
    if actual!=expected:raise ValueError('Existing immutable index differs from validated variants')
    return 'reuse'

def main():
    p=argparse.ArgumentParser();p.add_argument('mode',choices=['image','index']);p.add_argument('files',nargs='+',type=Path);a=p.parse_args();values=[json.loads(f.read_text()) for f in a.files]
    if a.mode=='image':
        if len(values)!=2:raise ValueError('image requires reference and actual inspect')
        verify_same_image(*values);print('identical')
    else:
        if len(values)!=3:raise ValueError('index requires index and two native variants')
        print(verify_index(*values))
if __name__=='__main__':main()
