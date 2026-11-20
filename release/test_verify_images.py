#!/usr/bin/env python3
import copy,importlib.util
from pathlib import Path
import unittest
s=importlib.util.spec_from_file_location('images',Path(__file__).with_name('verify_images.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
class ContentIdentity(unittest.TestCase):
 def info(self):return {'Os':'linux','Architecture':'amd64','Config':{'User':'65532:65532','Entrypoint':['/narbe'],'Labels':{'revision':'same'}},'RootFS':{'Type':'layers','Layers':['sha256:'+'a'*64]}}
 def variant(self,d):return {'digest':'sha256:'+d*64,'manifest':{'schemaVersion':2,'config':{'digest':'sha256:'+'b'*64},'layers':[{'digest':'sha256:'+'a'*64}]}}
 def index(self):return {'digest':'sha256:'+'f'*64,'manifest':{'manifests':[{'platform':{'os':'linux','architecture':a},'digest':'sha256:'+d*64} for a,d in [('amd64','c'),('arm64','d')]]}}
 def test_same_content_accepts_only_optional_docker_defaults(self):
  a=self.info();b=copy.deepcopy(a);b['Config'].update(AttachStdin=False,Hostname='',Cmd=None);m.verify_same_image(a,[b]);self.assertEqual(m.canonical_config({'StopTimeout':0}),{'StopTimeout':0})
 def test_claimed_labels_do_not_authorize_different_rootfs_or_config(self):
  for mutate in [lambda v:v['RootFS'].update(Layers=['sha256:'+'b'*64]),lambda v:v['Config'].update(Entrypoint=['/other']),lambda v:v.update(Architecture='arm64'),lambda v:v['Config'].update(User='0')]:
   a=self.info();b=copy.deepcopy(a);mutate(b)
   with self.assertRaises(ValueError):m.verify_same_image(a,b)
 def test_missing_or_invalid_identity_is_rejected(self):
  for field in ['Config','RootFS','Architecture','Os']:
   a=self.info();del a[field]
   with self.assertRaises(ValueError):m.image_identity(a)
 def test_absent_or_identical_index_can_be_safely_resumed(self):
  self.assertEqual(m.verify_index(None,self.variant('c'),self.variant('d')),'absent');self.assertEqual(m.verify_index(self.index(),self.variant('c'),self.variant('d')),'reuse')
 def test_index_wrong_platform_digest_duplicate_or_missing_rejected(self):
  for mutate in [lambda v:v['manifest']['manifests'].pop(),lambda v:v['manifest']['manifests'][0].update(digest='sha256:'+'e'*64),lambda v:v['manifest']['manifests'][1]['platform'].update(architecture='amd64'),lambda v:v.update(digest='bad')]:
   v=self.index();mutate(v)
   with self.assertRaises(ValueError):m.verify_index(v,self.variant('c'),self.variant('d'))
 def test_variant_must_not_be_a_nested_unverified_index(self):
  v=self.variant('c');v['manifest']=self.index()['manifest']
  with self.assertRaises(ValueError):m.verify_index(None,v,self.variant('d'))
if __name__=='__main__':unittest.main()
