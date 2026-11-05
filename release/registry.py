#!/usr/bin/env python3
"""Read GHCR metadata without disclosing auth tokens. No registry writes."""
import argparse
import base64
import json
import os
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY="pixingzoudaiyuexing/narbe"
ACCEPT="application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json"
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args,**kwargs):return None

def manifest(tag,anonymous=False):
    opener=urllib.request.build_opener(NoRedirect())
    headers={}
    token=os.environ.get("GH_TOKEN","")
    if token and not anonymous:
        identity=os.environ.get("GITHUB_ACTOR","pixingzoudaiyuexing")
        headers["Authorization"]="Basic "+base64.b64encode((identity+":"+token).encode()).decode()
    endpoint="https://ghcr.io/token?"+urllib.parse.urlencode({"service":"ghcr.io","scope":"repository:"+REPOSITORY+":pull"})
    try:
        with opener.open(urllib.request.Request(endpoint,headers=headers),timeout=30) as res:
            bearer=json.load(res)["token"]
        req=urllib.request.Request("https://ghcr.io/v2/"+REPOSITORY+"/manifests/"+urllib.parse.quote(tag,safe=":"),headers={"Authorization":"Bearer "+bearer,"Accept":ACCEPT})
        with opener.open(req,timeout=30) as res:
            return {"digest":res.headers["Docker-Content-Digest"],"manifest":json.load(res)}
    except urllib.error.HTTPError as error:
        if error.code==404:return None
        raise RuntimeError("GHCR metadata request failed, HTTP "+str(error.code)) from None
    except (OSError,KeyError,ValueError):
        raise RuntimeError("GHCR metadata request could not be verified") from None

def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("tag")
    parser.add_argument("--absent",action="store_true")
    parser.add_argument("--anonymous",action="store_true")
    args=parser.parse_args()
    data=manifest(args.tag,args.anonymous)
    if args.absent:
        if data is not None:raise SystemExit("Existing immutable version: refuse overwrite "+args.tag)
        print("Verified absent: "+args.tag)
    else:
        print(json.dumps(data,ensure_ascii=False))
if __name__=="__main__":main()
