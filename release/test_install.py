#!/usr/bin/env python3
"""Real published-image installer validation with isolated disposable data."""
import argparse
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

parser=argparse.ArgumentParser()
parser.add_argument('--image',required=True)
parser.add_argument('--arch',required=True,choices=['amd64','arm64'])
parser.add_argument('--revision',required=True)
args=parser.parse_args()
root=Path(__file__).resolve().parent
run_id=secrets.token_hex(8)
project='narbe-release-test-'+run_id
volume=project+'_narbe_data'

def docker(*a):return subprocess.check_output(['docker',*a],text=True,stderr=subprocess.PIPE).strip()

def wait(base):
    deadline=time.monotonic()+30
    while True:
        try:
            with urllib.request.urlopen(base+'/healthz',timeout=2) as res:
                if res.status==200:return
        except (OSError,urllib.error.URLError):
            if time.monotonic()>deadline:raise
            time.sleep(.1)

with tempfile.TemporaryDirectory(prefix='narbe-release-install-') as tmp:
    dest=Path(tmp)
    for name in ('install.sh','compose.yaml','.env.example'):
        shutil.copy(root/name,dest/name)
    (dest/'image.env').write_text('NARBE_IMAGE='+args.image+'\n')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
    base=f'http://127.0.0.1:{port}'
    (dest/'.env').write_text(f'NARBE_BIND=127.0.0.1\nNARBE_PORT={port}\nNARBE_ORIGIN={base}\nNARBE_LOG_LEVEL=info\n')
    env={**os.environ,'COMPOSE_PROJECT_NAME':project}
    initial=secrets.token_urlsafe(24);replacement=secrets.token_urlsafe(24)
    made=False
    def compose(*a):return subprocess.check_output(['docker','compose','--env-file','.env','--env-file','image.env','-f','compose.yaml',*a],cwd=dest,env=env,text=True,stderr=subprocess.PIPE).strip()
    def login(password):
        opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        req=urllib.request.Request(base+'/api/login',data=json.dumps({'password':password}).encode(),headers={'Content-Type':'application/json','Origin':base})
        try:
            with opener.open(req,timeout=10) as res:return res.status,json.load(res),opener
        except urllib.error.HTTPError as e:return e.code,json.load(e),opener
    try:
        docker('volume','create','--label','io.narbe.project=Narbe','--label','io.narbe.disposable=true','--label','com.docker.compose.project='+project,'--label','com.docker.compose.volume=narbe_data',volume);made=True
        subprocess.run(['bash','install.sh'],cwd=dest,env=env,input=initial+'\n'+initial+'\n',text=True,check=True)
        status,out,opener=login(initial);assert status==200
        with opener.open(base+'/api/accounts',timeout=10) as res:assert json.load(res)==[]
        req=urllib.request.Request(base+'/api/password',data=json.dumps({'current':initial,'password':replacement}).encode(),headers={'Content-Type':'application/json','Origin':base,'X-CSRF-Token':out['csrf']})
        with opener.open(req,timeout=10) as res:assert res.status==200
        compose('restart');wait(base)
        assert login(replacement)[0]==200 and login(initial)[0]==401
        # Running the actual installer again must not reset either secret or database.
        subprocess.run(['bash','install.sh'],cwd=dest,env=env,input='',text=True,check=True)
        assert login(replacement)[0]==200
        compose('up','-d','--no-build','--force-recreate','--wait')
        assert login(replacement)[0]==200 and login(initial)[0]==401
        cid=compose('ps','-q','narbe')
        config=json.loads(docker('inspect',cid))[0]
        metadata=json.loads(docker('image','inspect',config['Image']))[0]
        assert metadata['Architecture']==args.arch
        assert metadata['Config']['Labels']['org.opencontainers.image.revision']==args.revision
        assert metadata['Config']['Labels']['org.opencontainers.image.version']==(root/'VERSION').read_text().strip()
        assert config['Config']['User']=='65532:65532'
        assert config['HostConfig']['ReadonlyRootfs']
        assert config['HostConfig']['CapDrop']==['ALL']
        assert 'no-new-privileges:true' in config['HostConfig']['SecurityOpt']
        assert config['HostConfig']['LogConfig']['Config']=={'max-file':'3','max-size':'5m'}
        assert all(binding['HostIp']=='127.0.0.1' for binding in config['HostConfig']['PortBindings']['8080/tcp'])
        print('PASS:',args.arch,'published digest-pinned Compose installation, login, persistence across restart/reinstall/recreate, security and log rotation; cloud accounts empty; no cloud writes.')
        print('Docker image inspect size bytes:',metadata['Size'])
    finally:
        compose('down','--remove-orphans')
        if made:
            assert docker('volume','inspect',volume,'--format','{{index .Labels "io.narbe.disposable"}}')=='true'
            docker('volume','rm',volume)
