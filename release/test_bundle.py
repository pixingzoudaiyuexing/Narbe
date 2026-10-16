#!/usr/bin/env python3
import importlib.util
import hashlib
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
    def test_registry_permission_failure_is_not_treated_as_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.HTTPError('https://ghcr.io/token',403,'forbidden',{},None)
            with self.assertRaisesRegex(RuntimeError,'HTTP 403'):registry.manifest('v0.1.0-beta.5',True)
    def test_registry_network_failure_is_not_treated_as_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.URLError('fixture network error')
            with self.assertRaisesRegex(RuntimeError,'could not be verified'):registry.manifest('v0.1.0-beta.5',True)
if __name__=='__main__':unittest.main()
