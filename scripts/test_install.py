#!/usr/bin/env python3
"""Run the real one-command installation with isolated disposable Compose resources."""
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import urllib.request

root=Path(__file__).resolve().parent.parent
run_id=secrets.token_hex(6)
project="narbe-validation-"+run_id
scratch=root/".local"/("install-"+run_id)
scratch.mkdir(parents=True,mode=0o700)
password_file=scratch/"password"
password=secrets.token_urlsafe(24)
password_file.write_text(password)
password_file.chmod(0o644)
override=scratch/"compose-test.yaml"
override.write_text('volumes:\n  narbe_data:\n    labels:\n      io.narbe.disposable: "true"\nservices:\n  narbe:\n    labels:\n      io.narbe.disposable: "true"\n')
with socket.socket() as sock:
    sock.bind(("127.0.0.1",0))
    port=sock.getsockname()[1]
base=f"http://127.0.0.1:{port}"
env={**os.environ,"COMPOSE_PROJECT_NAME":project,"COMPOSE_FILE":str(root/"compose.yaml")+os.pathsep+str(override),"NARBE_PASSWORD_FILE":str(password_file),"NARBE_PORT":str(port),"NARBE_BIND":"127.0.0.1","NARBE_ORIGIN":base}
had_env=(root/".env").exists()
def compose(*args):return subprocess.check_output(["docker","compose",*args],cwd=root,env=env,text=True,stderr=subprocess.PIPE).strip()
volume=None
try:
    subprocess.run(["bash","scripts/start.sh"],cwd=root,env=env,check=True)
    config=json.loads(compose("config","--format","json"))
    volume=config["volumes"]["narbe_data"]["name"]
    cid=compose("ps","-q","narbe")
    assert cid
    jar=http.cookiejar.CookieJar()
    opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    req=urllib.request.Request(base+"/api/login",data=json.dumps({"password":password}).encode(),headers={"Origin":base,"Content-Type":"application/json"})
    with opener.open(req,timeout=10) as res:
        assert res.status==200
        assert json.load(res)["csrf"]
    with opener.open(base+"/api/accounts",timeout=10) as res:assert json.load(res)==[]
    inspect=json.loads(subprocess.check_output(["docker","inspect",cid],text=True))[0]
    assert inspect["HostConfig"]["LogConfig"]["Config"]["max-size"]=="5m"
    assert inspect["HostConfig"]["LogConfig"]["Config"]["max-file"]=="3"
    assert inspect["HostConfig"]["ReadonlyRootfs"]
    assert inspect["Config"]["User"]=="65532:65532"
    print("PASS: real scripts/start.sh build + smoke + Compose one-command install, initial auth, protected API, non-root/read-only/log rotation; isolated disposable project; no cloud credentials.")
finally:
    if volume is None:
        try:volume=json.loads(compose("config","--format","json"))["volumes"]["narbe_data"]["name"]
        except Exception:pass
    compose("down","--remove-orphans")
    if volume:
        probe=subprocess.run(["docker","volume","inspect",volume,"--format",'{{index .Labels "io.narbe.disposable"}}'],capture_output=True,text=True)
        if probe.returncode==0:
            assert probe.stdout.strip()=="true"
            subprocess.run(["docker","volume","rm",volume],check=True,stdout=subprocess.DEVNULL)
    if not had_env and (root/".env").read_bytes()==(root/".env.example").read_bytes():(root/".env").unlink()
    password_file.unlink(missing_ok=True)
    override.unlink(missing_ok=True)
    scratch.rmdir()
