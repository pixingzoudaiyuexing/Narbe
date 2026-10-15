#!/usr/bin/env python3
import importlib.util
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
    def test_bundle_pins_version_digest_without_secrets_or_development_files(self):
        with tempfile.TemporaryDirectory() as tmp:
            tmp=Path(tmp);record=tmp/'record.json';record.write_text(json.dumps({'version':'v0.1.0-beta.3','image':'ghcr.io/pixingzoudaiyuexing/narbe','digest':'sha256:'+'a'*64,'sourceSha':'b'*40,'applicationReviewedSha':'4f1c9cc2a7c8e099ab3c375548c48e691d87c72d','releaseReviewedSha':'c'*40}))
            subprocess.run(['python3',str(root/'bundle.py'),str(record),str(tmp/'out')],check=True,cwd=root.parent,stdout=subprocess.DEVNULL)
            with tarfile.open(tmp/'out/narbe-v0.1.0-beta.3-install.tar.gz') as tar:
                names=tar.getnames();self.assertEqual(set(names),{'compose.yaml','install.sh','.env.example','README.md','VERSION','image.env','release.json'})
                self.assertIn(b'v0.1.0-beta.3@sha256:',tar.extractfile('image.env').read())
                self.assertNotIn(b'build:',tar.extractfile('compose.yaml').read())
                self.assertNotIn(b'latest',tar.extractfile('image.env').read())
                self.assertFalse(any(name.startswith('/') or '..' in Path(name).parts for name in names))
                self.assertEqual(tar.getmember('install.sh').mode,0o755)
    def test_registry_permission_failure_is_not_treated_as_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.HTTPError('https://ghcr.io/token',403,'forbidden',{},None)
            with self.assertRaisesRegex(RuntimeError,'HTTP 403'):registry.manifest('v0.1.0-beta.3',True)
    def test_registry_network_failure_is_not_treated_as_absence(self):
        with mock.patch.object(registry.urllib.request,'build_opener') as opener:
            opener.return_value.open.side_effect=urllib.error.URLError('fixture network error')
            with self.assertRaisesRegex(RuntimeError,'could not be verified'):registry.manifest('v0.1.0-beta.3',True)
if __name__=='__main__':unittest.main()
