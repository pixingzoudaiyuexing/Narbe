#!/usr/bin/env python3
"""Real beta.1 -> candidate Compose upgrade on a disposable Narbe volume only."""
import argparse
import hashlib
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import sqlite3
import io
import tarfile
import subprocess
import tempfile
import urllib.error
import urllib.request

parser=argparse.ArgumentParser()
parser.add_argument('--old-image',required=True)
parser.add_argument('--new-image',required=True)
a=parser.parse_args()
project='narbe-upgrade-test-'+secrets.token_hex(8)
volume=project+'_narbe_data'
root=Path(__file__).resolve().parent.parent

def docker(*args):return subprocess.check_output(['docker',*args],text=True,stderr=subprocess.PIPE).strip()

with tempfile.TemporaryDirectory(prefix='narbe-upgrade-') as tmp:
    d=Path(tmp)
    shutil.copy(root/'release/compose.yaml',d/'compose.yaml')
    (d/'secrets').mkdir(mode=0o700)
    initial=secrets.token_urlsafe(24);replacement=secrets.token_urlsafe(24)
    (d/'secrets/admin-password').write_text(initial);(d/'secrets/admin-password').chmod(0o644)
    with socket.socket() as sock:sock.bind(('127.0.0.1',0));port=sock.getsockname()[1]
    base=f'http://127.0.0.1:{port}'
    (d/'.env').write_text(f'NARBE_BIND=127.0.0.1\nNARBE_PORT={port}\nNARBE_ORIGIN={base}\n')
    env={**os.environ,'COMPOSE_PROJECT_NAME':project}
    made=False
    def compose(*args):return subprocess.check_output(['docker','compose','--env-file','.env','--env-file','image.env','-f','compose.yaml',*args],cwd=d,env=env,text=True,stderr=subprocess.PIPE).strip()
    def login(password):
        op=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        req=urllib.request.Request(base+'/api/login',data=json.dumps({'password':password}).encode(),headers={'Origin':base,'Content-Type':'application/json'})
        try:
            with op.open(req,timeout=10) as res:return res.status,json.load(res),op
        except urllib.error.HTTPError as e:return e.code,json.load(e),op
    def read_snapshot(name):
        backup=d/name
        backup.mkdir(mode=0o700)
        compose('cp','narbe:/data',str(backup/'data'))
        key=hashlib.sha256((backup/'data/master.key').read_bytes()).hexdigest()
        db=sqlite3.connect('file:'+str(backup/'data/narbe.db')+'?mode=ro',uri=True)
        settings=db.execute('SELECT key,value FROM settings ORDER BY key').fetchall()
        accounts=db.execute('SELECT id,provider,credentials,revision,retired FROM accounts ORDER BY id').fetchall()
        db.close()
        return key,settings,accounts
    try:
        (d/'image.env').write_text('NARBE_IMAGE='+a.old_image+'\n')
        docker('volume','create','--label','io.narbe.project=Narbe','--label','io.narbe.disposable=true','--label','com.docker.compose.project='+project,'--label','com.docker.compose.volume=narbe_data',volume);made=True
        compose('up','-d','--no-build','--pull','never','--wait')
        status,out,op=login(initial);assert status==200
        req=urllib.request.Request(base+'/api/password',data=json.dumps({'current':initial,'password':replacement}).encode(),headers={'Origin':base,'Content-Type':'application/json','X-CSRF-Token':out['csrf']})
        with op.open(req,timeout=10) as res:assert res.status==200
        old_id=compose('ps','-q','narbe')
        old=json.loads(docker('inspect',old_id))[0]
        assert next(m['Name'] for m in old['Mounts'] if m['Destination']=='/data')==volume
        compose('stop')
        read_snapshot('fixture')
        subprocess.run(['go','test','./internal/store','-run','^TestDockerUpgradeFixture$','-count=1'],cwd=root,env={**env,'NARBE_DOCKER_UPGRADE_FIXTURE':str(d/'fixture/data')},check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        archive=io.BytesIO()
        with tarfile.open(fileobj=archive,mode='w') as tar:
            for file in (d/'fixture/data').iterdir():
                assert file.is_file() and file.name in {'narbe.db','narbe.db-wal','narbe.db-shm','master.key','.lock'}
                info=tar.gettarinfo(str(file),arcname=file.name);info.uid=info.gid=65532;info.uname=info.gname=''
                with file.open('rb') as data:tar.addfile(info,data)
        subprocess.run(['docker','cp','-a','-',old_id+':/data'],input=archive.getvalue(),check=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        compose('up','-d','--no-build','--pull','never','--wait')
        status,_,op=login(replacement);assert status==200
        with op.open(base+'/api/accounts',timeout=10) as res:
            accounts=json.load(res);assert len(accounts)==1 and accounts[0]['id']=='upgrade-fixture'
        compose('stop')
        before=read_snapshot('before')
        assert len(before[2])==1
        (d/'image.env').write_text('NARBE_IMAGE='+a.new_image+'\n')
        compose('up','-d','--no-build','--pull','never','--wait')
        assert login(replacement)[0]==200 and login(initial)[0]==401
        new_id=compose('ps','-q','narbe');assert new_id!=old_id
        new=json.loads(docker('inspect',new_id))[0]
        assert next(m['Name'] for m in new['Mounts'] if m['Destination']=='/data')==volume
        assert new['Config']['User']=='65532:65532' and new['HostConfig']['ReadonlyRootfs']
        assert new['HostConfig']['CapDrop']==['ALL']
        assert 'no-new-privileges:true' in new['HostConfig']['SecurityOpt']
        assert new['HostConfig']['LogConfig']['Config']=={'max-size':'5m','max-file':'3'}
        compose('stop')
        after=read_snapshot('after');assert before==after,'master.key, password/settings or credential rows changed'
        compose('up','-d','--no-build','--pull','never','--force-recreate','--wait')
        assert login(replacement)[0]==200
        print('PASS: real beta.1 -> candidate Compose upgrade and recreation preserve same volume, master.key, password/settings and account credential rows; health/non-root/read-only/security/log rotation verified; no cloud operations.')
    finally:
        compose('down','--remove-orphans')
        if made:
            assert docker('volume','inspect',volume,'--format','{{index .Labels "io.narbe.disposable"}}')=='true'
            assert docker('volume','inspect',volume,'--format','{{index .Labels "com.docker.compose.project"}}')==project
            docker('volume','rm',volume)
