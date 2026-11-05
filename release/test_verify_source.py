#!/usr/bin/env python3
"""Adversarial source-gate tests in disposable Git repositories; no Docker/network."""
import importlib.util
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT=Path(__file__).resolve().parent.parent
WORKFLOW=(ROOT/'.github/workflows/release.yml').read_text()
PUBLISH=(ROOT/'release/publish.sh').read_text()
VERIFIER=(ROOT/'release/verify_source.py').read_text()
spec = importlib.util.spec_from_file_location('verify_source', Path(__file__).with_name('verify_source.py'))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class SourceGate(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='narbe-release-gate-')
        self.previous = Path.cwd()
        os.chdir(self.tmp.name)
        self.addCleanup(self.cleanup)
        self.run_git('init', '-q')
        self.run_git('config', 'user.name', 'Narbe fixture')
        self.run_git('config', 'user.email', 'fixture@example.invalid')
        for name in set(gate.RELEASE_FILES) | set(['cmd/narbe/main.go', 'internal/cloud/linode.go', 'go.mod', 'Dockerfile',
                     'scripts/test_runtime.py', 'release/install.sh', 'release/registry.py',
                     '.github/workflows/release.yml', 'release/publish.sh', 'release/README.md', 'release/NOTES.md']):
            self.write(name, 'reviewed original\n')
        Path('release/publish.sh').chmod(0o755)
        self.write('release/VERSION', 'v0.1.0-beta.4\n')
        self.app = self.save()
        self.original_app = gate.APPROVED_APPLICATION
        # Only fixtures use a generated approved anchor, never a repo variable.
        gate.APPROVED_APPLICATION = self.app
        self.addCleanup(setattr, gate, 'APPROVED_APPLICATION', self.original_app)
        self.original_evidence = gate.APPROVED_EVIDENCE
        for path in gate.APP_EVIDENCE_FILES:
            self.write(path, '{"approved": true}\n' if path.endswith('.json') else 'Approved beta.5 delivery evidence\n')
        self.evidence = self.save()
        gate.APPROVED_EVIDENCE = self.evidence
        self.addCleanup(setattr, gate, 'APPROVED_EVIDENCE', self.original_evidence)
        self.write('release/VERSION', gate.VERSION + '\n')
        self.write('release/publish.sh', 'reviewed beta5 publish\n')
        self.write('.github/workflows/release.yml', 'reviewed dual gate workflow\n')
        self.write('release/verify_source.py', VERIFIER.replace(self.original_app, self.app).replace(self.original_evidence, self.evidence))
        self.release = self.save()

    def cleanup(self):
        os.chdir(self.previous)
        self.tmp.cleanup()

    def run_git(self, *args):
        return subprocess.check_output(['git', *args], stderr=subprocess.PIPE).decode().strip()

    def write(self, name, text):
        path = Path(name)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def save(self):
        self.run_git('add', '-A')
        self.run_git('commit', '-qm', 'fixture')
        return self.run_git('rev-parse', 'HEAD')

    def check(self, head=None, release=None, app=None):
        return gate.verify(app or self.app, release or self.release, head or self.run_git('rev-parse', 'HEAD'))

    def test_approved_release_and_only_explicit_text_updates_pass(self):
        self.assertEqual(self.check()['releaseReviewedSha'], self.release)
        self.assertEqual(self.check()['deliveryEvidenceSha'], self.evidence)
        self.assertEqual(self.check()['applicationEvidenceChanges'], sorted(gate.APP_EVIDENCE_FILES))
        self.write('release/NOTES.md', 'Final Chinese release text\n')
        self.write('release/README.md', 'Final installation instructions\n')
        head = self.save()
        self.assertEqual(self.check(head)['postReviewTextChanges'], ['release/NOTES.md', 'release/README.md'])

    def test_evidence_allowlist_is_exact_and_frozen_after_approved_delivery(self):
        self.assertEqual(gate.APP_EVIDENCE_FILES, frozenset({
            'docs/BETA5_DELIVERY.md', 'docs/BETA5_SOURCES.md', 'docs/BETA5_EVIDENCE.json'}))
        for path in [*gate.APP_EVIDENCE_FILES, 'docs/other.md', 'docs/BETA5_EVIDENCE.json.py']:
            with self.subTest(path=path):
                self.run_git('checkout', '-q', '--detach', self.release)
                self.write(path, 'unapproved release-stage evidence change\n')
                head = self.save()
                with self.assertRaisesRegex(ValueError, 'Unapproved application'):
                    self.check(head, release=head)

    def test_evidence_anchor_cannot_contain_unrelated_code_or_lookalike_paths(self):
        for path in ['docs/other.md', 'docs/BETA5_DELIVERY.md.sh', 'internal/cloud/linode.go']:
            with self.subTest(path=path):
                self.run_git('checkout', '-q', '--detach', self.evidence)
                self.write(path, 'unreviewed injection\n')
                bad = self.save()
                previous = gate.APPROVED_EVIDENCE
                try:
                    gate.APPROVED_EVIDENCE = bad
                    with self.assertRaisesRegex(ValueError, 'Unapproved delivery evidence'):
                        self.check(head=bad, release=bad)
                finally:
                    gate.APPROVED_EVIDENCE = previous

    def test_evidence_type_mode_text_and_json_bypasses_are_rejected(self):
        for path in sorted(gate.APP_EVIDENCE_FILES):
            for kind in ['symlink', 'executable', 'delete', 'directory', 'binary', 'invalid-utf8', 'invalid-json']:
                if kind == 'invalid-json' and not path.endswith('.json'):
                    continue
                with self.subTest(path=path, kind=kind):
                    self.run_git('checkout', '-q', '--detach', self.evidence)
                    p = Path(path)
                    if kind in ['symlink', 'delete', 'directory']: p.unlink()
                    if kind == 'symlink': p.symlink_to('../release/publish.sh')
                    elif kind == 'executable': p.chmod(0o755)
                    elif kind == 'directory': p.mkdir(); (p/'payload').write_text('injection')
                    elif kind == 'binary': p.write_bytes(b'notes\x00payload')
                    elif kind == 'invalid-utf8': p.write_bytes(b'\xff\xfe')
                    elif kind == 'invalid-json': p.write_text('not json')
                    bad = self.save()
                    self.write('release/VERSION', gate.VERSION + '\n')
                    candidate = self.save()
                    previous = gate.APPROVED_EVIDENCE
                    try:
                        gate.APPROVED_EVIDENCE = bad
                        with self.assertRaises((ValueError, UnicodeError)):
                            self.check(head=candidate, release=candidate)
                    finally:
                        gate.APPROVED_EVIDENCE = previous

    def test_evidence_anchor_must_be_commit_on_both_ancestry_edges(self):
        previous = gate.APPROVED_EVIDENCE
        try:
            for invalid in ['', 'a'*39, 'f'*40, self.run_git('rev-parse', 'HEAD^{tree}')]:
                gate.APPROVED_EVIDENCE = invalid
                with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                    self.check()
            self.run_git('checkout', '-q', '--detach', self.app)
            self.write('docs/BETA5_DELIVERY.md', 'sibling delivery\n')
            gate.APPROVED_EVIDENCE = self.save()
            with self.assertRaisesRegex(ValueError, 'ancestor'):
                self.check(head=self.release)
        finally:
            gate.APPROVED_EVIDENCE = previous

    def test_application_changes_cannot_hide_in_release_review(self):
        for name in ['cmd/narbe/main.go', 'internal/cloud/linode.go', 'go.mod', 'Dockerfile',
                     'scripts/test_runtime.py', 'release/install.sh', 'release/registry.py',
                     'docs/new-executable.sh', 'go.sum', '.dockerignore', 'package-lock.json', 'internal/web/assets/injected.js', '.github/workflows/new.yml']:
            with self.subTest(path=name):
                self.run_git('checkout', '-q', '--detach', self.release)
                self.write(name, 'unreviewed mutation\n')
                head = self.save()
                with self.assertRaisesRegex(ValueError, 'Unapproved application'):
                    self.check(head, release=head)

    def test_executable_workflow_or_nonwhitelisted_changes_after_release_review_fail(self):
        for name in ['release/publish.sh', 'release/verify_source.py', 'release/VERSION',
                     '.github/workflows/release.yml', '.github/workflows/ci.yml', 'docs/BETA5_RELEASE_REVIEW.md', *gate.APP_EVIDENCE_FILES]:
            with self.subTest(path=name):
                self.run_git('checkout', '-q', '--detach', self.release)
                self.write(name, 'unreviewed post-review mutation\n')
                head = self.save()
                with self.assertRaisesRegex(ValueError, 'Unreviewed executable'):
                    self.check(head)

    def test_invalid_missing_wrong_application_and_missing_release_sha_fail(self):
        for sha in ['', 'main', 'a' * 39, 'A' * 40, ';touch /tmp/injected', 'f' * 40]:
            for index in range(3):
                with self.subTest(sha=sha,argument=index):
                    args=[self.app,self.release,self.release];args[index]=sha
                    with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                        gate.verify(*args)
        with self.assertRaisesRegex(ValueError, 'Application SHA'):
            gate.verify(self.release, self.release, self.release)

    def test_nonancestor_head_and_source_mismatch_fail(self):
        self.run_git('checkout', '-q', '--detach', self.app)
        self.write('release/NOTES.md', 'sibling branch\n')
        sibling = self.save()
        with self.assertRaisesRegex(ValueError, 'ancestor'):
            self.check(sibling)
        self.run_git('checkout', '-q', '--detach', self.release)
        with self.assertRaisesRegex(ValueError, 'Checked-out HEAD'):
            self.write('release/NOTES.md', 'new text commit\n')
            self.save()
            self.check(head=self.release)

    def test_symlink_or_executable_text_exception_is_rejected(self):
        for mode in ['symlink', 'executable']:
            with self.subTest(mode=mode):
                self.run_git('checkout', '-q', '--detach', self.release)
                path = Path('release/NOTES.md')
                if mode == 'symlink':
                    path.unlink()
                    path.symlink_to('publish.sh')
                else:
                    path.chmod(0o755)
                head = self.save()
                with self.assertRaisesRegex(ValueError, 'regular file type/mode'):
                    self.check(head)

    def test_release_review_cannot_change_file_types_or_remove_required_files(self):
        for path in ['release/publish.sh', 'release/verify_source.py', '.github/workflows/release.yml']:
            for mutation in ['symlink', 'mode', 'delete']:
                with self.subTest(path=path, mutation=mutation):
                    self.run_git('checkout', '-q', '--detach', self.release)
                    p=Path(path)
                    if mutation=='symlink':
                        p.unlink();p.symlink_to('README.md')
                    elif mutation=='delete':p.unlink()
                    else:p.chmod(0o644 if path.endswith('.sh') else 0o755)
                    head=self.save()
                    with self.assertRaisesRegex(ValueError, 'regular file type/mode'):
                        self.check(head, release=head)

    def test_binary_text_exception_is_rejected(self):
        for raw in [b'notes\x00payload', b'notes\x1b[31m', b'\xff\xfe']:
            with self.subTest(raw=raw):
                self.run_git('checkout', '-q', '--detach', self.release)
                Path('release/README.md').write_bytes(raw)
                head=self.save()
                with self.assertRaises((ValueError, UnicodeError)):
                    self.check(head)

    def test_git_objects_must_be_commits_and_both_ancestry_edges_hold(self):
        for obj in [self.run_git('rev-parse', 'HEAD^{tree}'),self.run_git('rev-parse','HEAD:release/README.md')]:
            with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                self.check(release=obj)
        self.run_git('tag','-am','fixture annotated','reviewed-tag',self.release)
        with self.assertRaisesRegex(ValueError,'commit directly'):
            self.check(release=self.run_git('rev-parse','reviewed-tag'))
        self.run_git('checkout','--orphan','unrelated')
        self.write('release/NOTES.md','unrelated history\n')
        unrelated=self.save()
        with self.assertRaisesRegex(ValueError,'ancestor'):
            self.check(head=unrelated,release=unrelated)

    def test_reviewed_verifier_cli_rejects_replacement_without_executing_it(self):
        # Same bootstrap as Workflow: extract from reviewed SHA, never HEAD.
        trusted=self.run_git('show',self.release+':release/verify_source.py')
        with tempfile.TemporaryDirectory() as tmp:
            script=Path(tmp)/'reviewed.py';script.write_text(trusted)
            def execute():
                return subprocess.run(['python3',str(script),'--application',self.app,'--release',self.release,'--head',self.run_git('rev-parse','HEAD')],capture_output=True,text=True)
            self.assertEqual(execute().returncode,0)
            self.write('release/verify_source.py', "from pathlib import Path\nPath('UNREVIEWED_EXECUTED').touch()\n")
            self.save()
            rejected=execute()
            self.assertNotEqual(rejected.returncode,0)
            self.assertIn('Unreviewed executable',rejected.stderr)
            self.assertFalse(Path('UNREVIEWED_EXECUTED').exists())

    def test_dirty_source_and_untracked_build_input_fail(self):
        self.write('internal/cloud/linode.go', 'dirty code\n')
        with self.assertRaisesRegex(ValueError, 'working tree'):
            self.check()
        self.run_git('checkout', '--', 'internal/cloud/linode.go')
        Path('.git/info/exclude').write_text('*.log\n')
        for name in ['internal/new.go','internal/注入.go','internal/web/assets/ignored.log','scripts/ignored.log','release/ignored.log','.github/workflows/ignored.log']:
            self.write(name, 'untracked injection\n')
            with self.assertRaisesRegex(ValueError, 'Untracked'):
                self.check()
            Path(name).unlink()


class OwnerSourceGate(unittest.TestCase):
    cleanup = SourceGate.cleanup
    run_git = SourceGate.run_git
    write = SourceGate.write
    save = SourceGate.save

    def setUp(self):
        SourceGate.setUp(self)
        self.original_baseline = gate.APPLICATION_BASELINE
        self.original_pr = gate.INTEGRATED_PR_HEAD
        gate.APPLICATION_BASELINE, gate.INTEGRATED_PR_HEAD = self.app, self.release
        self.addCleanup(setattr, gate, 'APPLICATION_BASELINE', self.original_baseline)
        self.addCleanup(setattr, gate, 'INTEGRATED_PR_HEAD', self.original_pr)
        for path in gate.OWNER_RELEASE_FILES:
            self.write(path, 'Owner release fixture\n')
        Path('release/publish.sh').chmod(0o755)
        self.write('release/VERSION', gate.VERSION+'\n')
        self.auth={'mode':gate.OWNER_MODE,'authorizedBy':'Owner','authorizationDate':'2026-11-05',
                   'manualIndependentReview':'waived-by-owner','applicationBaselineSha':self.app,
                   'integratedPRHead':self.release}
        import json
        self.write('release/OWNER_AUTHORIZATION.json', json.dumps(self.auth))
        self.write('release/verify_source.py', VERIFIER.replace(self.original_baseline,self.app).replace(self.original_pr,self.release))
        self.authorized=self.save()

    def owner(self, head=None, authorized=None):
        return gate.verify_owner(authorized or self.authorized, head or self.run_git('rev-parse','HEAD'))

    def test_owner_waiver_passes_without_claiming_independent_review(self):
        record=self.owner()
        self.assertEqual(record['sourceSha'],self.authorized)
        self.assertEqual(record['releaseMode'],gate.OWNER_MODE)
        self.assertEqual(record['ownerAuthorization']['manualIndependentReview'],'waived-by-owner')
        self.assertNotIn('applicationReviewedSha',record)
        self.assertNotIn('releaseReviewedSha',record)

    def test_even_later_notes_need_new_exact_source_and_new_ci(self):
        self.write('release/NOTES.md','later text\n');head=self.save()
        with self.assertRaisesRegex(ValueError,'exact source SHA'):self.owner(head)
        self.assertEqual(self.owner(head,authorized=head)['sourceSha'],head)

    def test_application_or_nonallowlisted_input_cannot_enter_owner_path(self):
        for path in ['cmd/narbe/main.go','internal/cloud/linode.go','Dockerfile','go.mod','go.sum','.dockerignore',
                     'scripts/test_runtime.py','internal/web/assets/extra.js','docs/arbitrary.py','release/install.sh']:
            with self.subTest(path=path):
                self.run_git('checkout','-q','--detach',self.authorized)
                self.write(path,'injected\n');head=self.save()
                with self.assertRaisesRegex(ValueError,'Unauthorized application'):self.owner(head,authorized=head)

    def test_invalid_sha_ancestry_and_old_review_sha_cannot_authorize(self):
        for value in ['', 'main', 'A'*40, 'f'*40, self.run_git('rev-parse','HEAD^{tree}')]:
            with self.assertRaises((ValueError,subprocess.CalledProcessError)):gate.verify_owner(value,self.authorized)
        with self.assertRaisesRegex(ValueError,'exact source SHA'):gate.verify_owner(self.app,self.authorized)
        self.run_git('checkout','-q','--detach',self.app)
        self.write('release/NOTES.md','sibling\n');sibling=self.save()
        with self.assertRaisesRegex(ValueError,'ancestor'):self.owner(sibling,authorized=sibling)

    def test_owner_file_modes_and_false_manual_pass_are_rejected(self):
        import json
        for path in ['release/OWNER_AUTHORIZATION.json','release/NOTES.md','release/verify_source.py','.github/workflows/release.yml']:
            for kind in ['symlink','mode','delete']:
                with self.subTest(path=path,kind=kind):
                    self.run_git('checkout','-q','--detach',self.authorized)
                    p=Path(path)
                    if kind=='symlink':p.unlink();p.symlink_to('README.md')
                    elif kind=='mode':p.chmod(0o755)
                    else:p.unlink()
                    head=self.save()
                    with self.assertRaisesRegex(ValueError,'regular file type/mode'):self.owner(head,authorized=head)
        for key,value in [('manualIndependentReview','PASS'),('authorizedBy','Reviewer'),('applicationReviewedSha',self.app)]:
            self.run_git('checkout','-q','--detach',self.authorized)
            auth=dict(self.auth);auth[key]=value
            self.write('release/OWNER_AUTHORIZATION.json',json.dumps(auth));head=self.save()
            with self.assertRaises(ValueError):self.owner(head,authorized=head)

    def test_dirty_ignored_and_untracked_owner_inputs_are_rejected(self):
        self.write('release/verify_source.py','dirty script\n')
        with self.assertRaisesRegex(ValueError,'working tree'):self.owner()
        self.run_git('checkout','--','release/verify_source.py')
        Path('.git/info/exclude').write_text('*.log\n')
        for path in ['internal/web/assets/injected.log','release/injected.log','scripts/injected.py']:
            self.write(path,'injection')
            with self.assertRaisesRegex(ValueError,'Untracked'):self.owner()
            Path(path).unlink()

    def test_authorized_verifier_cli_rejects_modified_source_without_executing_it(self):
        trusted=self.run_git('show',self.authorized+':release/verify_source.py')
        with tempfile.TemporaryDirectory() as tmp:
            script=Path(tmp)/'authorized.py';script.write_text(trusted)
            def run(head):return subprocess.run(['python3',str(script),'--owner-authorized-source',self.authorized,'--head',head],capture_output=True,text=True)
            self.assertEqual(run(self.authorized).returncode,0)
            self.write('release/verify_source.py',"from pathlib import Path\nPath('UNAUTHORIZED_EXECUTED').touch()\n")
            head=self.save();self.assertNotEqual(run(head).returncode,0)
            self.assertFalse(Path('UNAUTHORIZED_EXECUTED').exists())


class WorkflowContract(unittest.TestCase):
    def test_publish_missing_owner_pin_and_old_review_values_stop_before_registry(self):
        with tempfile.TemporaryDirectory() as tmp:
            marker=Path(tmp)/'registry-called'
            docker=Path(tmp)/'docker';docker.write_text('#!/bin/sh\ntouch "'+str(marker)+'"\nexit 0\n');docker.chmod(0o755)
            for overrides in [{},{'AUTHORIZED_SOURCE_SHA':''},{'AUTHORIZED_SOURCE_SHA':'f'*40},
                              {'APP_REVIEWED_SHA':gate.APPROVED_APPLICATION,'RELEASE_REVIEWED_SHA':gate.APPROVED_EVIDENCE}]:
                env={'PATH':tmp+os.pathsep+os.environ['PATH'],'GITHUB_SHA':gate.APPROVED_APPLICATION,'RUNNER_TEMP':tmp,**overrides}
                result=subprocess.run(['bash',str(ROOT/'release/publish.sh')],cwd=ROOT,env=env,capture_output=True)
                self.assertNotEqual(result.returncode,0);self.assertFalse(marker.exists())

    def test_historical_anchors_not_relabelled_and_owner_baseline_is_exact(self):
        self.assertEqual(gate.APPROVED_APPLICATION,'182e57759494c6fbcf6e3291d8259b524a9ea49a')
        self.assertEqual(gate.APPLICATION_BASELINE,'3216f1e9e5151338427c7ceee5fe0090fdebf1e4')
        self.assertEqual(gate.INTEGRATED_PR_HEAD,'ec66fd4e90f38a1940b8cdf662868bd1a5a0a70e')

    def test_old_review_variables_cannot_enable_owner_release(self):
        self.assertNotIn('_REVIEWED_SHA',WORKFLOW)
        self.assertIn('vars.NARBE_BETA5_OWNER_AUTHORIZED_SHA',WORKFLOW)
        self.assertIn("vars.NARBE_BETA5_OWNER_AUTHORIZED_SHA != ''",WORKFLOW)
        self.assertIn('test "$AUTHORIZED_SOURCE_SHA" = "$GITHUB_SHA"',WORKFLOW)
        self.assertIn('narbe-v0.1.0-beta.5-release',WORKFLOW)

    def test_package_write_waits_for_source_exact_ci_and_both_native_builds(self):
        publish=WORKFLOW.split('  publish:',1)[1].split('  verify-published:',1)[0]
        self.assertIn('needs: [source-gate, verify-ci, build]',publish)
        for dependency in ['source-gate','verify-ci','build']:
            self.assertIn("needs."+dependency+".result == 'success'",publish)
        self.assertIn("needs.source-gate.outputs.approved == 'true'",publish)
        self.assertEqual(WORKFLOW.count('packages: write'),1)
        self.assertLess(publish.index('Recheck Owner-authorized scripts'),publish.index('GH_TOKEN:'))
        ci=WORKFLOW.split('  verify-ci:',1)[1].split('  build:',1)[0]
        self.assertIn('actions: read',ci);self.assertNotIn('packages: write',ci)
        self.assertIn('release/verify_ci.py --sha "$GITHUB_SHA"',ci)

    def test_authorized_verifier_extracted_before_login_and_ci_evidence_checked(self):
        self.assertIn('git show "$AUTHORIZED_SOURCE_SHA:release/verify_source.py"',WORKFLOW)
        self.assertNotIn('python3 release/verify_source.py',WORKFLOW)
        self.assertLess(PUBLISH.index('narbe-authorized-source.py'),PUBLISH.index('docker login'))
        self.assertLess(PUBLISH.index("record['sourceSha']==os.environ['GITHUB_SHA']"),PUBLISH.index('docker login'))
        self.assertIn('ownerAuthorization=',PUBLISH)
        self.assertIn('automatedValidation=',PUBLISH)
        self.assertNotIn('applicationReviewedSha=',PUBLISH)
        self.assertNotIn('releaseReviewedSha=',PUBLISH)
        self.assertNotIn('"$image:latest"',PUBLISH)

    def test_native_published_installs_pull_anonymously_and_keep_beta4_upgrade(self):
        self.assertIn('ubuntu-24.04-arm',WORKFLOW)
        self.assertIn('amd64:x86_64|arm64:aarch64',WORKFLOW)
        self.assertIn('release/test_install.py --image "$image"',WORKFLOW)
        self.assertIn('v0.1.0-beta.4@sha256:1d1f45b4',WORKFLOW)
        pulls=WORKFLOW.split('  verify-published:',1)[1].split('  bundle:',1)[0]
        self.assertNotIn('docker login',pulls);self.assertNotIn('GH_TOKEN',pulls)
        self.assertIn('test ! -e "$DOCKER_CONFIG/config.json"',pulls)

    def test_failed_pipeline_cannot_create_git_tag_or_github_release(self):
        self.assertNotIn('contents: write',WORKFLOW)
        self.assertNotRegex(WORKFLOW+PUBLISH,r'\b(?:gh\s+release|git\s+tag|git\s+push)\b')
        self.assertIn('needs: verify-published',WORKFLOW)
        self.assertIn('python3 release/bundle.py',WORKFLOW)

    def test_actions_pinned_and_only_push_release_entry(self):
        import re
        for action in re.findall(r'uses:\s+(\S+)',WORKFLOW):self.assertRegex(action,r'^[\w/-]+@[a-f0-9]{40}$')
        self.assertNotIn('pull_request',WORKFLOW);self.assertNotIn('workflow_dispatch',WORKFLOW)


if __name__ == '__main__':
    unittest.main()

