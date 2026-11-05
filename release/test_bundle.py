#!/usr/bin/env python3
import importlib.util
import hashlib
import copy
import json
import subprocess
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock
import urllib.error

root=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('registry',root/'registry.py');registry=importlib.util.module_from_spec(spec);spec.loader.exec_module(registry)
class Packaging(unittest.TestCase):
    def record(self):
        r = {'version':'v0.1.0-beta.5','image':'ghcr.io/pixingzoudaiyuexing/narbe','digest':'sha256:'+'a'*64,'sourceSha':'b'*40,'applicationReviewedSha':'182e57759494c6fbcf6e3291d8259b524a9ea49a','releaseReviewedSha':'c'*40,'workflowUrl':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/123','manifest':{'manifests':[{'platform':{'os':'linux','architecture':a},'digest':'sha256:'+d*64} for a,d in [('amd64','d'),('arm64','e')]]}}
        r['deliveryEvidenceSha']='800c5443cb5fb913e30fe71467642c6fdfa87b00'
        r['sourceGate']={key:r[key] for key in ('version','sourceSha','applicationReviewedSha','releaseReviewedSha','deliveryEvidenceSha')}
        r['sourceGate'].update(applicationEvidenceChanges=['docs/BETA5_DELIVERY.md','docs/BETA5_EVIDENCE.json','docs/BETA5_SOURCES.md'],releaseConfigurationChanges=['release/VERSION'],postReviewTextChanges=[])
        return r

    def test_bundle_pins_version_digest_without_secrets_or_development_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp);record=tmp/'record.json';record.write_text(json.dumps(self.record()))
            subprocess.run(['python3',str(root/'bundle.py'),str(record),str(tmp/'out')],check=True,cwd=root.parent,stdout=subprocess.DEVNULL)
            with tarfile.open(tmp/'out/narbe-v0.1.0-beta.5-install.tar.gz') as tar:
                names=tar.getnames();self.assertEqual(set(names),{'compose.yaml','install.sh','.env.example','README.md','VERSION','image.env','release.json'})
                self.assertIn(b'v0.1.0-beta.5@sha256:',tar.extractfile('image.env').read())
                self.assertNotIn(b'build:',tar.extractfile('compose.yaml').read())
                self.assertNotIn(b'latest',tar.extractfile('image.env').read())
                self.assertFalse(any(name.startswith('/') or '..' in Path(name).parts for name in names))
                self.assertEqual(tar.getmember('install.sh').mode,0o755)
                self.assertEqual(json.loads(tar.extractfile('release.json').read()),self.record())
            archive=tmp/'out/narbe-v0.1.0-beta.5-install.tar.gz'
            self.assertEqual((tmp/'out/SHA256SUMS').read_text(),hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')
            self.assertEqual(json.loads((tmp/'out/release-evidence.json').read_text()),self.record())

    def test_bundle_rejects_wrong_version_review_digest_platforms_and_provenance(self):
        mutations={'deliveryEvidenceSha':'b'*40,'sourceGate':{},'version':'v0.1.0-beta.4','applicationReviewedSha':'4f1c9cc2a7c8e099ab3c375548c48e691d87c72d','releaseReviewedSha':'main','sourceSha':'bad','digest':'sha256:bad','image':'ghcr.io/other/narbe','workflowUrl':'https://example.invalid/run','manifest':{'manifests':[]}}
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp)
            for field,value in mutations.items():
                with self.subTest(field=field):
                    record=self.record();record[field]=value
                    p=tmp/'record.json';p.write_text(json.dumps(record))
                    result=subprocess.run(['python3',str(root/'bundle.py'),str(p),str(tmp/'out')],cwd=root.parent,capture_output=True)
                    self.assertNotEqual(result.returncode,0)
                    self.assertFalse((tmp/'out').exists())
    def owner_record(self):
        r=self.record()
        for key in ('applicationReviewedSha','releaseReviewedSha','deliveryEvidenceSha'):
            del r[key]
        r.update(releaseMode='owner-authorized-manual-review-waiver',authorizedSourceSha=r['sourceSha'],applicationBaselineSha='3216f1e9e5151338427c7ceee5fe0090fdebf1e4',integratedPRHead='ec66fd4e90f38a1940b8cdf662868bd1a5a0a70e')
        r['ownerAuthorization']={'mode':r['releaseMode'],'authorizedBy':'Owner','authorizationDate':'2026-11-05','manualIndependentReview':'waived-by-owner','applicationBaselineSha':r['applicationBaselineSha'],'integratedPRHead':r['integratedPRHead']}
        r['sourceGate']={key:r[key] for key in ('releaseMode','sourceSha','authorizedSourceSha','applicationBaselineSha','integratedPRHead','ownerAuthorization')}
        r['automatedValidation']={'sourceSha':r['sourceSha'],'conclusion':'success','workflowUrl':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/122','jobs':[{'name':name,'conclusion':'success','stepCount':count,'url':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/122/job/'+str(job)} for name,count,job in [('verify',30,1),('arm64-runtime',13,2)]]}
        return r

    def run_record(self,record):
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp);p=tmp/'record.json';p.write_text(json.dumps(record))
            result=subprocess.run(['python3',str(root/'bundle.py'),str(p),str(tmp/'out')],cwd=root.parent,capture_output=True)
            return result.returncode,(tmp/'out').exists(),result.stderr.decode()

    def test_owner_waiver_bundle_records_exact_source_and_truthful_authorization(self):
        code,exists,error=self.run_record(self.owner_record())
        self.assertEqual(code,0,error)
        self.assertTrue(exists)

    def test_owner_waiver_rejects_forged_source_or_independent_review_claim(self):
        valid=self.owner_record()
        variants=[]
        for key,value in [('sourceSha','main'),('authorizedSourceSha','c'*40),('applicationBaselineSha','b'*40),('integratedPRHead','b'*40),('releaseMode','independently-reviewed'),('applicationReviewedSha','b'*40),('releaseReviewedSha','b'*40),('independentReview',{'conclusion':'PASS'}),('manualIndependentReview','PASS'),('digest','sha256:bad'),('version','v0.1.0-beta.4'),('image','ghcr.io/other/narbe'),('workflowUrl','https://githubXcom/pixingzoudaiyuexing/Narbe/actions/runs/123'),('manifest',{'manifests':[]})]:
            r=copy.deepcopy(valid);r[key]=value;variants.append((key,r))
        for key,value in [('authorizedBy','Independent Reviewer'),('authorizationDate','2026-11-04'),('manualIndependentReview','PASS'),('mode','independently-reviewed'),('applicationBaselineSha','b'*40)]:
            r=copy.deepcopy(valid);r['ownerAuthorization'][key]=value;r['sourceGate']['ownerAuthorization']=r['ownerAuthorization'];variants.append(('authorization '+key,r))
        for key in ('ownerAuthorization','sourceGate','authorizedSourceSha','automatedValidation'):
            r=copy.deepcopy(valid);del r[key];variants.append(('missing '+key,r))
        r=copy.deepcopy(valid);r['sourceGate']['sourceSha']='c'*40;variants.append(('source-gate mismatch',r))
        r=copy.deepcopy(valid);r['sourceGate']['applicationReviewedSha']='b'*40;variants.append(('source-gate false review',r))
        for name,r in variants:
            with self.subTest(name=name):
                code,exists,_=self.run_record(r);self.assertNotEqual(code,0);self.assertFalse(exists)

    def test_owner_waiver_rejects_incomplete_skipped_or_wrong_source_ci(self):
        valid=self.owner_record()
        variants=[]
        for key,value in [('sourceSha','c'*40),('conclusion','failure'),('workflowUrl','https://example.invalid/run'),('jobs',[])]:
            r=copy.deepcopy(valid);r['automatedValidation'][key]=value;variants.append((key,r))
        for field,value in [('conclusion','skipped'),('stepCount',0),('stepCount',True),('stepCount','30'),('url','https://example.invalid/job')]:
            r=copy.deepcopy(valid);r['automatedValidation']['jobs'][0][field]=value;variants.append((field,r))
        r=copy.deepcopy(valid);r['automatedValidation']['jobs'].pop();variants.append(('missing native ARM64 job',r))
        r=copy.deepcopy(valid);r['automatedValidation']['jobs'].append({'name':'unexpected','conclusion':'failure','stepCount':1,'url':'https://github.com/pixingzoudaiyuexing/Narbe/actions/runs/122/job/3'});variants.append(('extra failed job',r))
        r=copy.deepcopy(valid);r['automatedValidation']['jobs'].append(copy.deepcopy(r['automatedValidation']['jobs'][0]));variants.append(('duplicate job',r))
        for name,r in variants:
            with self.subTest(name=name):
                code,exists,_=self.run_record(r);self.assertNotEqual(code,0);self.assertFalse(exists)

    def test_validation_cannot_be_disabled_by_python_optimization(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp)
            for make_record in (self.record,self.owner_record):
                r=make_record();r['digest']='sha256:bad'
                p=tmp/'record.json';p.write_text(json.dumps(r))
                result=subprocess.run(['python3','-O',str(root/'bundle.py'),str(p),str(tmp/'out')],cwd=root.parent,capture_output=True)
                self.assertNotEqual(result.returncode,0)
                self.assertFalse((tmp/'out').exists())

    def test_registry_permission_failure_is_not_treated_as_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.HTTPError('https://ghcr.io/token',403,'forbidden',{},None)
            with self.assertRaisesRegex(RuntimeError,'HTTP 403'):registry.manifest('v0.1.0-beta.5',True)
    def test_registry_network_failure_is_not_treated_as_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.URLError('fixture network error')
            with self.assertRaisesRegex(RuntimeError,'could not be verified'):registry.manifest('v0.1.0-beta.5',True)
if __name__=='__main__':unittest.main()
