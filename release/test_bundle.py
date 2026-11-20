#!/usr/bin/env python3
"""Beta.6 release provenance, manager/Probe content, and registry failure tests."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.error

root=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('registry',root/'registry.py');registry=importlib.util.module_from_spec(spec);spec.loader.exec_module(registry)
spec=importlib.util.spec_from_file_location('bundle',root/'bundle.py');bundle=importlib.util.module_from_spec(spec);spec.loader.exec_module(bundle)

class Packaging(unittest.TestCase):
    def record(self):
        sha='b'*40
        auth={'mode':bundle.MODE,'authorizedBy':'Owner','authorizationDate':'2026-11-20','authorizationText':'发布新的测试版本','manualIndependentReview':'not-performed-for-this-candidate','applicationBaselineSha':bundle.BASELINE,'version':bundle.VERSION}
        r={'version':bundle.VERSION,'image':bundle.IMAGE,'digest':'sha256:'+'a'*64,'sourceSha':sha,'authorizedSourceSha':sha,'releaseMode':bundle.MODE,'applicationBaselineSha':bundle.BASELINE,'ownerAuthorization':auth,'workflowUrl':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/123'}
        r['manifest']={'manifests':[{'platform':{'os':'linux','architecture':a},'digest':'sha256:'+d*64} for a,d in [('amd64','d'),('arm64','e')]]}
        r['probe']={'image':bundle.IMAGE,'tag':bundle.VERSION+'-probe','sourceSha':sha,'digest':'sha256:'+'f'*64,'manifest':copy.deepcopy(r['manifest'])}
        r['sourceGate']={k:r[k] for k in ('releaseMode','sourceSha','authorizedSourceSha','applicationBaselineSha','ownerAuthorization','version')}
        r['automatedValidation']={'sourceSha':sha,'conclusion':'success','workflowPath':'.github/workflows/ci.yml','workflowUrl':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/122','jobs':[{'name':name,'conclusion':'success','stepCount':count,'url':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/122/job/'+str(job)} for name,count,job in [('verify',30,1),('arm64-runtime',13,2)]]}
        return r
    def run_record(self,record,optimize=False):
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp);p=tmp/'record.json';p.write_text(json.dumps(record))
            command=['python3']+(['-O'] if optimize else [])+[str(root/'bundle.py'),str(p),str(tmp/'out')]
            result=subprocess.run(command,cwd=root.parent,capture_output=True)
            return result.returncode,(tmp/'out').exists(),result.stderr.decode()
    def test_bundle_pins_both_images_and_only_installation_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp);r=self.record();p=tmp/'record.json';p.write_text(json.dumps(r))
            subprocess.run(['python3',str(root/'bundle.py'),str(p),str(tmp/'out')],cwd=root.parent,check=True,stdout=subprocess.DEVNULL)
            archive=tmp/'out'/('narbe-'+bundle.VERSION+'-install.tar.gz')
            with tarfile.open(archive) as tar:
                self.assertEqual(set(tar.getnames()),{'compose.yaml','install.sh','.env.example','README.md','VERSION','image.env','probe-image.env','probe-compose.yaml','install-probe.sh','release.json'})
                self.assertEqual(tar.extractfile('image.env').read(),f"NARBE_IMAGE={bundle.IMAGE}:{bundle.VERSION}@{r['digest']}\n".encode())
                self.assertEqual(tar.extractfile('probe-image.env').read(),f"NARBE_PROBE_IMAGE={bundle.IMAGE}:{bundle.VERSION}-probe@{r['probe']['digest']}\n".encode())
                for name in ['compose.yaml','probe-compose.yaml']:self.assertNotIn(b'build:',tar.extractfile(name).read())
                for name in ['install.sh','install-probe.sh']:self.assertEqual(tar.getmember(name).mode,0o755)
                self.assertEqual(json.loads(tar.extractfile('release.json').read()),r)
            checks=(tmp/'out/SHA256SUMS').read_text().splitlines();self.assertEqual(len(checks),2)
            for line in checks:
                digest,name=line.split('  ');self.assertEqual(hashlib.sha256((tmp/'out'/name).read_bytes()).hexdigest(),digest)
    def test_invalid_source_authorization_and_false_review_claims_are_rejected(self):
        r=self.record()
        for field,value in [('version','v0.1.0-beta.5'),('image','ghcr.io/other/narbe'),('sourceSha','main'),('authorizedSourceSha','c'*40),('applicationBaselineSha','c'*40),('releaseMode','owner-authorized-manual-review-waiver'),('applicationReviewedSha','b'*40),('releaseReviewedSha','b'*40),('independentReview','PASS'),('sourceGate',{}),('workflowUrl','https://example.invalid/run')]:
            with self.subTest(field=field):
                bad=copy.deepcopy(r);bad[field]=value;code,exists,_=self.run_record(bad);self.assertNotEqual(code,0);self.assertFalse(exists)
        for field,value in [('authorizationDate','2026-11-05'),('authorizationText','old authorization'),('manualIndependentReview','PASS'),('manualIndependentReview','waived-by-owner'),('authorizedBy','Reviewer')]:
            bad=copy.deepcopy(r);bad['ownerAuthorization'][field]=value;code,exists,_=self.run_record(bad);self.assertNotEqual(code,0);self.assertFalse(exists)
    def test_missing_wrong_probe_digest_platform_and_source_reject_before_output(self):
        for field,value in [('tag','v0.1.0-beta.5-probe'),('digest','sha256:bad'),('manifest',{'manifests':[]}),('sourceSha','c'*40),('image','ghcr.io/other/probe')]:
            bad=self.record();bad['probe'][field]=value;code,exists,_=self.run_record(bad);self.assertNotEqual(code,0);self.assertFalse(exists)
        for key in ['probe','ownerAuthorization','sourceGate','automatedValidation']:
            bad=self.record();del bad[key];code,exists,_=self.run_record(bad);self.assertNotEqual(code,0);self.assertFalse(exists)
    def test_manager_index_wrong_arch_duplicate_or_invalid_digest_rejects(self):
        for change in [lambda r:r.update(digest='sha256:bad'),lambda r:r['manifest'].update(manifests=[]),lambda r:r['manifest']['manifests'][0]['platform'].update(architecture='arm64'),lambda r:r['manifest']['manifests'][0].update(digest='bad')]:
            r=self.record();change(r);self.assertNotEqual(self.run_record(r)[0],0)
    def test_incomplete_skipped_or_wrong_source_ci_is_rejected(self):
        for field,value in [('sourceSha','c'*40),('conclusion','failure'),('workflowPath','other.yml'),('workflowUrl','https://example.invalid/run'),('jobs',[])]:
            r=self.record();r['automatedValidation'][field]=value;self.assertNotEqual(self.run_record(r)[0],0)
        for field,value in [('conclusion','skipped'),('stepCount',0),('stepCount',True),('stepCount','30'),('url','https://example.invalid/job')]:
            r=self.record();r['automatedValidation']['jobs'][0][field]=value;self.assertNotEqual(self.run_record(r)[0],0)
        for kind in ['missing','duplicate','extra']:
            r=self.record();jobs=r['automatedValidation']['jobs']
            if kind=='missing':jobs.pop()
            elif kind=='duplicate':jobs[1]=copy.deepcopy(jobs[0])
            else:jobs.append(copy.deepcopy(jobs[0]))
            self.assertNotEqual(self.run_record(r)[0],0)
    def test_validation_cannot_be_disabled_by_optimization(self):
        r=self.record();r['probe']['digest']='bad';code,exists,_=self.run_record(r,True);self.assertNotEqual(code,0);self.assertFalse(exists)
    def test_registry_permission_failure_is_not_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.HTTPError('https://ghcr.io/token',403,'forbidden',{},None)
            with self.assertRaisesRegex(RuntimeError,'HTTP 403'):registry.manifest(bundle.VERSION,True)
    def test_registry_network_failure_is_not_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.URLError('network error')
            with self.assertRaisesRegex(RuntimeError,'could not be verified'):registry.manifest(bundle.VERSION,True)
if __name__=='__main__':unittest.main()
