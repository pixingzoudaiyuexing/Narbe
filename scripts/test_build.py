#!/usr/bin/env python3
"""Exercise cleanup decisions with a stateful Docker CLI double; never touches Docker."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

FAKE = r'''#!/usr/bin/env python3
import json,os,sys
p=os.environ['FAKE_STATE'];s=json.load(open(p));a=sys.argv[1:];s['commands'].append(a)
code=0;out=''
if a[:1]==['info']:pass
elif a[:2]==['buildx','inspect']:code=0 if s.get('builder') else 1
elif a[:2]==['buildx','create']:
 s['builder']=True
 if s['scenario']=='builder_create_fail':code=1
elif a[:2]==['buildx','build']:
 candidate=a[a.index('-t')+1];s['candidate']=candidate;s['images'][candidate]='new'
 if s['scenario']=='build_fail':code=1
elif a[:2]==['buildx','rm']:
 if s['scenario']=='cleanup_fail':code=1
 else:s['builder']=False
elif a[:1]==['run']:
 s['container']=True
 if s['scenario']=='run_fail':code=1
 else:out='own-smoke-id'
elif a[:1]==['exec']:
 if s['scenario']=='health_fail':code=1
elif a[:1]==['ps']:
 filt=a[a.index('--filter')+1]
 if filt.startswith('label=') and s.get('container'):out='own-smoke-id'
 elif filt=='ancestor=old' and (s['scenario']=='referenced' or (s['scenario']=='upgrade' and not s.get('replaced'))):out='protected-stopped-container'
 elif filt.startswith('ancestor=narbe/local:candidate') and s.get('container'):out='own-smoke-id'
elif a[:1]==['rm']:
 assert a[-1]=='own-smoke-id',a
 s['container']=False
elif a[:1]==['tag']:s['images'][a[2]]=s['images'][a[1]]
elif a[:2]==['image','ls']:
 if s['scenario']=='inspect_fail' and '-q' in a and a[-1].startswith('narbe/local:candidate'):code=1
 elif s['scenario']=='collector_fail' and '-a' in a:code=1
 elif '-q' in a:out=s['images'].get(a[-1],'')
 else:out='\n'.join(set(s['images'].values())|({'old'} if 'old' not in s['removed'] else set()))
elif a[:2]==['image','inspect']:
 key=a[2];fmt=a[a.index('--format')+1] if '--format' in a else ''
 if s['scenario']=='old_inspect_fail' and key=='narbe/local:current':code=1
 elif key not in s['images'] and key not in ('old','new'):code=1
 elif 'RepoTags' in fmt:out='narbe/local:rollback-kept' if s['scenario']=='rollback' else ''
 elif 'io.narbe.project' in fmt:out='Narbe' if key!='other' else 'other'
 elif '.Id' in fmt:out=s['images'].get(key,key)
 elif 'Validated' in fmt:out='Validated new bytes=123'
elif a[:2]==['image','rm']:
 key=a[2];s['removed'].append(key);s['images'].pop(key,None)
else:raise Exception('unhandled docker command '+str(a))
json.dump(s,open(p,'w'));print(out) if out else None;sys.exit(code)
'''

class BuildSafety(unittest.TestCase):
    def run_case(self, scenario):
        with tempfile.TemporaryDirectory(prefix="narbe-cli-test-") as tmp:
            root = Path(tmp)
            (root / "scripts").mkdir()
            (root / "bin").mkdir()
            shutil.copy(Path(__file__).with_name("build.sh"), root / "scripts/build.sh")
            shutil.copy(Path(__file__).with_name("collect-images.sh"),root / "scripts/collect-images.sh")
            docker = root / "bin/docker"
            docker.write_text(FAKE)
            docker.chmod(0o755)
            sleep = root / "bin/sleep"
            sleep.write_text("#!/bin/sh\nexit 0\n")
            sleep.chmod(0o755)
            state_file = root / "state.json"
            state_file.write_text(json.dumps({"scenario":scenario,"images":{"narbe/local:current":"old","other/project:keep":"other"},"commands":[],"removed":[],"builder":False,"container":False}))
            env = {**os.environ,"PATH":str(root / "bin")+os.pathsep+os.environ["PATH"],"FAKE_STATE":str(state_file)}
            result = subprocess.run(["bash",str(root / "scripts/build.sh")],env=env,capture_output=True,text=True,timeout=15)
            state = json.loads(state_file.read_text())
            self.assertEqual(state["images"].get("other/project:keep"),"other")
            self.assertFalse(state["container"],result.stderr)
            self.assertFalse(any(p.is_dir() for p in (root / ".local").glob("build-*")))
            self.assertFalse(any("prune" in command or "--volumes" in command for command in state["commands"]))
            if scenario!="inspect_fail":self.assertFalse(any(k.startswith("narbe/local:candidate") for k in state["images"]))
            if scenario == "inspect_fail":
                self.assertNotEqual(result.returncode,0)
            elif scenario == "cleanup_fail":
                self.assertNotEqual(result.returncode,0)
                self.assertTrue(state["builder"])
                self.assertIn("清理失败",result.stderr)
            else:
                self.assertFalse(state["builder"],result.stderr)
            if scenario=="upgrade":
                self.assertNotIn("old",state["removed"])
                state["replaced"]=True
                state_file.write_text(json.dumps(state))
                collected=subprocess.run(["bash",str(root / "scripts/collect-images.sh")],env=env,capture_output=True,text=True,timeout=10)
                self.assertEqual(collected.returncode,0,collected.stderr)
                state=json.loads(state_file.read_text())
                self.assertIn("old",state["removed"],"superseded image leaked after compose replacement")
            if scenario in ("inspect_fail","collector_fail"):
                self.assertTrue((root / ".local/cleanup-pending").exists())
                before=len(state["commands"])
                retry=subprocess.run(["bash",str(root / "scripts/build.sh")],env=env,capture_output=True,text=True,timeout=10)
                self.assertNotEqual(retry.returncode,0)
                subsequent=json.loads(state_file.read_text())["commands"][before:]
                self.assertFalse(any(command[:2]==["buildx","create"] for command in subsequent),"new build started before cleanup verified")
            return result,state

    def test_success_removes_only_exact_replaced_image(self):
        result,state=self.run_case("success")
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertIn("old",state["removed"])
        self.assertEqual(state["images"]["narbe/local:current"],"new")

    def test_rollback_image_is_preserved(self):
        result,state=self.run_case("rollback")
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertNotIn("old",state["removed"])

    def test_stopped_container_reference_is_preserved(self):
        result,state=self.run_case("referenced")
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertNotIn("old",state["removed"])

    def test_partial_failures_do_not_accumulate_candidates(self):
        for scenario in ("build_fail","run_fail","health_fail","builder_create_fail"):
            with self.subTest(scenario=scenario):
                result,state=self.run_case(scenario)
                self.assertNotEqual(result.returncode,0)
                self.assertNotIn("old",state["removed"])
                self.assertEqual(state["images"]["narbe/local:current"],"old")

    def test_cleanup_failure_is_not_reported_as_success(self):
        self.run_case("cleanup_fail")

    def test_collector_failure_blocks_new_build(self):
        result,_=self.run_case("collector_fail")
        self.assertNotEqual(result.returncode,0)

    def test_initial_query_failure_stops_before_builder(self):
        result,state=self.run_case("old_inspect_fail")
        self.assertNotEqual(result.returncode,0)
        self.assertFalse(any(command[:2]==["buildx","create"] for command in state["commands"]))

    def test_upgrade_collects_after_container_replacement(self):
        result,_=self.run_case("upgrade")
        self.assertEqual(result.returncode,0)

    def test_inventory_error_does_not_claim_cleanup_success(self):
        self.run_case("inspect_fail")

if __name__=="__main__":
    unittest.main()
