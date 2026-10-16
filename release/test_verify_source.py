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


class WorkflowContract(unittest.TestCase):
    def test_publish_missing_beta5_review_values_stops_before_any_registry_command(self):
        with tempfile.TemporaryDirectory() as tmp:
            marker=Path(tmp)/'registry-called'
            docker=Path(tmp)/'docker';docker.write_text('#!/bin/sh\ntouch "'+str(marker)+'"\nexit 0\n');docker.chmod(0o755)
            for overrides in [{},{'APP_REVIEWED_SHA':gate.APPROVED_APPLICATION},
                              {'APP_REVIEWED_SHA':gate.APPROVED_APPLICATION,'RELEASE_REVIEWED_SHA':''},
                              {'RELEASE_REVIEWED_SHA':'f'*40},
                              {'APP_REVIEWED_SHA':'ebc933f2842be343ffcb6eefd240cdab54ef1b4d','RELEASE_REVIEWED_SHA':'5432c681bc66ec5d8f46914ad5cbc7dbd9fd3a04'}]:
                env={'PATH':tmp+os.pathsep+os.environ['PATH'],'GITHUB_SHA':gate.APPROVED_APPLICATION,'RUNNER_TEMP':tmp,'NARBE_BETA4_APP_REVIEWED_SHA':'ebc933f2842be343ffcb6eefd240cdab54ef1b4d','NARBE_BETA4_RELEASE_REVIEWED_SHA':'5432c681bc66ec5d8f46914ad5cbc7dbd9fd3a04',**overrides}
                result=subprocess.run(['bash',str(ROOT/'release/publish.sh')],cwd=ROOT,env=env,capture_output=True)
                self.assertNotEqual(result.returncode,0)
                self.assertFalse(marker.exists())

    def test_application_anchor_is_the_exact_formally_approved_head(self):
        self.assertEqual(gate.APPROVED_APPLICATION,'182e57759494c6fbcf6e3291d8259b524a9ea49a')
        self.assertEqual(gate.APPROVED_EVIDENCE,'800c5443cb5fb913e30fe71467642c6fdfa87b00')

    def test_old_review_variable_cannot_enable_new_release(self):
        self.assertNotIn('NARBE_BETA2_REVIEWED_SHA', WORKFLOW)
        self.assertNotIn('NARBE_BETA3_', WORKFLOW)
        self.assertNotIn('NARBE_BETA4_', WORKFLOW)
        self.assertIn('vars.NARBE_BETA5_APP_REVIEWED_SHA', WORKFLOW)
        self.assertIn('vars.NARBE_BETA5_RELEASE_REVIEWED_SHA', WORKFLOW)
        self.assertIn('narbe-v0.1.0-beta.5-release', WORKFLOW)
        self.assertIn("vars.NARBE_BETA5_APP_REVIEWED_SHA != '' && vars.NARBE_BETA5_RELEASE_REVIEWED_SHA != ''",WORKFLOW)

    def test_package_write_job_waits_for_unprivileged_gate_and_both_builds(self):
        gate_job=WORKFLOW.split('  source-gate:',1)[1].split('  build:',1)[0]
        self.assertIn('contents: read',gate_job)
        self.assertNotIn('packages: write',gate_job)
        publish=WORKFLOW.split('  publish:',1)[1].split('  verify-published:',1)[0]
        self.assertIn('needs: [source-gate, build]',publish)
        self.assertIn("if: needs.source-gate.result == 'success' && needs.build.result == 'success' && needs.source-gate.outputs.approved == 'true'",publish)
        self.assertEqual(WORKFLOW.count('packages: write'),1)
        self.assertLess(publish.index('Recheck reviewed scripts'),publish.index('GH_TOKEN:'))

    def test_reviewed_verifier_is_extracted_before_token_and_login(self):
        self.assertIn('git show "$RELEASE_REVIEWED_SHA:release/verify_source.py"',WORKFLOW)
        self.assertNotIn('python3 release/verify_source.py',WORKFLOW)
        self.assertLess(PUBLISH.index('narbe-reviewed-source.py'),PUBLISH.index('docker login'))
        self.assertIn('applicationReviewedSha=',PUBLISH)
        self.assertIn('releaseReviewedSha=',PUBLISH)
        self.assertNotIn('"$image:latest"',PUBLISH)

    def test_native_published_installs_keep_pinned_previous_release_upgrade(self):
        self.assertIn('ubuntu-24.04-arm',WORKFLOW)
        self.assertIn('amd64:x86_64|arm64:aarch64',WORKFLOW)
        self.assertIn('release/test_install.py --image "$image"',WORKFLOW)
        self.assertIn('scripts/test_upgrade.py --old-image',WORKFLOW)
        self.assertIn('v0.1.0-beta.4@sha256:1d1f45b4',WORKFLOW)
        self.assertNotIn('v0.1.0-beta.3@',WORKFLOW)

    def test_failed_release_pipeline_cannot_create_git_tag_or_github_release(self):
        self.assertNotIn('contents: write', WORKFLOW)
        self.assertNotRegex(WORKFLOW + PUBLISH, r'\b(?:gh\s+release|git\s+tag|git\s+push)\b')
        self.assertIn('needs: verify-published', WORKFLOW)
        self.assertIn('python3 release/bundle.py', WORKFLOW)

    def test_all_actions_are_sha_pinned_and_release_cannot_run_on_pull_request(self):
        import re
        for action in re.findall(r'uses:\s+(\S+)', WORKFLOW):
            self.assertRegex(action, r'^[\w/-]+@[a-f0-9]{40}$')
        self.assertNotIn('pull_request', WORKFLOW)
        self.assertNotIn('workflow_dispatch', WORKFLOW)


if __name__ == '__main__':
    unittest.main()

