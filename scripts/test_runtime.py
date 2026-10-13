#!/usr/bin/env python3
"""Real Docker persistence test using fresh, explicitly disposable local data."""
import http.cookiejar
import json
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def docker(*args):
    return subprocess.check_output(["docker",*args],text=True,stderr=subprocess.PIPE).strip()


def main():
    run_id=secrets.token_hex(6)
    container="narbe-validation-"+run_id
    volume="narbe-validation-data-"+run_id
    image=os.environ.get("NARBE_IMAGE","narbe/local:current")
    assert docker("image","inspect",image,"--format",'{{index .Config.Labels "io.narbe.project"}}')=="Narbe"
    with tempfile.TemporaryDirectory(prefix="narbe-runtime-") as tmp:
        password=secrets.token_urlsafe(24)
        replacement=secrets.token_urlsafe(24)
        password_file=Path(tmp)/"password"
        password_file.write_text(password)
        password_file.chmod(0o644)
        made_volume=False
        try:
            docker("volume","create","--label","io.narbe.project=Narbe","--label","io.narbe.disposable=true",volume)
            made_volume=True
            def launch():
                docker("run","-d","--name",container,"--label","io.narbe.project=Narbe","--label","io.narbe.disposable=true","--read-only","--cap-drop","ALL","--security-opt","no-new-privileges:true","--memory","384m","--pids-limit","128","--log-driver","json-file","--log-opt","max-size=5m","--log-opt","max-file=3","-p","127.0.0.1::8080","-v",volume+":/data","--mount",f"type=bind,src={password_file},dst=/run/password,readonly","-e","NARBE_ADMIN_PASSWORD_FILE=/run/password",image)
                port=docker("port",container,"8080/tcp").split(":")[-1]
                base="http://127.0.0.1:"+port
                deadline=time.monotonic()+20
                while True:
                    try:
                        with urllib.request.urlopen(base+"/healthz",timeout=2) as res:
                            if res.status==200:break
                    except (OSError,urllib.error.URLError):
                        if time.monotonic()>deadline:raise
                        time.sleep(0.1)
                jar=http.cookiejar.CookieJar()
                opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
                return base,opener
            def request(base,opener,path,body,csrf=""):
                req=urllib.request.Request(base+"/api/"+path,data=json.dumps(body).encode(),headers={"Content-Type":"application/json","Origin":base,"X-CSRF-Token":csrf})
                try:
                    with opener.open(req,timeout=10) as res:return res.status,json.load(res)
                except urllib.error.HTTPError as e:return e.code,json.load(e)
            base,opener=launch()
            status,out=request(base,opener,"login",{"password":password});assert status==200
            status,_=request(base,opener,"password",{"current":password,"password":replacement},out["csrf"]);assert status==200
            old_base=base
            docker("restart",container)
            base="http://127.0.0.1:"+docker("port",container,"8080/tcp").split(":")[-1]
            print("Docker restart changed ephemeral host port:",old_base!=base)
            deadline=time.monotonic()+20
            while True:
                try:
                    with urllib.request.urlopen(base+"/healthz",timeout=2) as res:
                        if res.status==200:break
                except (OSError,urllib.error.URLError):
                    if time.monotonic()>deadline:raise
                    time.sleep(0.1)
            status,_=request(base,opener,"login",{"password":replacement});assert status==200
            status,_=request(base,opener,"login",{"password":password});assert status==401
            docker("rm","-f",container)
            base,opener=launch()
            status,_=request(base,opener,"login",{"password":replacement});assert status==200
            status,_=request(base,opener,"login",{"password":password});assert status==401
            config=json.loads(docker("inspect",container))[0]
            assert config["Config"]["User"]=="65532:65532"
            assert config["HostConfig"]["ReadonlyRootfs"]
            assert config["HostConfig"]["LogConfig"]["Config"]["max-size"]=="5m"
            assert config["HostConfig"]["LogConfig"]["Config"]["max-file"]=="3"
            if os.environ.get("NARBE_EXTRACT_BINARY"):
                docker("cp",container+":/narbe",os.environ["NARBE_EXTRACT_BINARY"])
            print("PASS: real Docker health, non-root/read-only/log rotation, SQLite password persistence across restart and container replacement; no cloud credentials or cloud operations.")
        finally:
            # Both identities were generated here and carry explicit disposable ownership.
            if subprocess.run(["docker","inspect",container],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL).returncode==0:
                assert docker("inspect",container,"--format",'{{index .Config.Labels "io.narbe.disposable"}}')=="true"
                docker("rm","-f",container)
            if made_volume:
                assert docker("volume","inspect",volume,"--format",'{{index .Labels "io.narbe.disposable"}}')=="true"
                docker("volume","rm",volume)

if __name__=="__main__":main()
