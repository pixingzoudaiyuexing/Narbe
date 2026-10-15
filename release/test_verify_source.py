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
        for name in ['cmd/narbe/main.go', 'internal/cloud/linode.go', 'go.mod', 'Dockerfile',
                     'scripts/test_runtime.py', 'release/install.sh', 'release/registry.py',
                     '.github/workflows/release.yml', 'release/publish.sh', 'release/README.md', 'release/NOTES.md']:
            self.write(name, 'reviewed original\n')
        self.write('release/VERSION', 'v0.1.0-beta.2\n')
        self.app = self.save()
        self.original_app = gate.APPROVED_APPLICATION
        # Only fixtures use a generated approved anchor, never a repo variable.
        gate.APPROVED_APPLICATION = self.app
        self.addCleanup(setattr, gate, 'APPROVED_APPLICATION', self.original_app)
        self.write('release/VERSION', gate.VERSION + '\n')
        self.write('release/publish.sh', 'reviewed beta3 publish\n')
        self.write('.github/workflows/release.yml', 'reviewed dual gate workflow\n')
        self.write('release/verify_source.py', 'reviewed verifier\n')
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
        self.write('release/NOTES.md', 'Final Chinese release text\n')
        self.write('release/README.md', 'Final installation instructions\n')
        head = self.save()
        self.assertEqual(self.check(head)['postReviewTextChanges'], ['release/NOTES.md', 'release/README.md'])

    def test_application_changes_cannot_hide_in_release_review(self):
        for name in ['cmd/narbe/main.go', 'internal/cloud/linode.go', 'go.mod', 'Dockerfile',
                     'scripts/test_runtime.py', 'release/install.sh', 'release/registry.py',
                     'docs/new-executable.sh', '.github/workflows/new.yml']:
            with self.subTest(path=name):
                self.run_git('checkout', '-q', '--detach', self.release)
                self.write(name, 'unreviewed mutation\n')
                head = self.save()
                with self.assertRaisesRegex(ValueError, 'Unapproved application'):
                    self.check(head, release=head)

    def test_executable_workflow_or_nonwhitelisted_changes_after_release_review_fail(self):
        for name in ['release/publish.sh', 'release/verify_source.py', 'release/VERSION',
                     '.github/workflows/release.yml', '.github/workflows/ci.yml', 'docs/BETA3_RELEASE_REVIEW.md']:
            with self.subTest(path=name):
                self.run_git('checkout', '-q', '--detach', self.release)
                self.write(name, 'unreviewed post-review mutation\n')
                head = self.save()
                with self.assertRaisesRegex(ValueError, 'Unreviewed executable'):
                    self.check(head)

    def test_invalid_missing_wrong_application_and_missing_release_sha_fail(self):
        for sha in ['', 'main', 'a' * 39, 'A' * 40, ';touch /tmp/injected', 'f' * 40]:
            with self.subTest(sha=sha):
                with self.assertRaises((ValueError, subprocess.CalledProcessError)):
                    gate.verify(self.app, sha, self.release)
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
                with self.assertRaisesRegex(ValueError, 'regular non-executable'):
                    self.check(head)

    def test_dirty_source_and_untracked_build_input_fail(self):
        self.write('internal/cloud/linode.go', 'dirty code\n')
        with self.assertRaisesRegex(ValueError, 'working tree'):
            self.check()
        self.run_git('checkout', '--', 'internal/cloud/linode.go')
        for name in ['internal/new.go','internal/注入.go']:
            self.write(name, 'untracked injection\n')
            with self.assertRaisesRegex(ValueError, 'Untracked'):
                self.check()
            Path(name).unlink()


class WorkflowContract(unittest.TestCase):
    def test_application_anchor_is_the_exact_formally_approved_head(self):
        self.assertEqual(gate.APPROVED_APPLICATION,'4f1c9cc2a7c8e099ab3c375548c48e691d87c72d')

    def test_old_review_variable_cannot_enable_new_release(self):
        self.assertNotIn('NARBE_BETA2_REVIEWED_SHA', WORKFLOW)
        self.assertIn('vars.NARBE_BETA3_APP_REVIEWED_SHA', WORKFLOW)
        self.assertIn('vars.NARBE_BETA3_RELEASE_REVIEWED_SHA', WORKFLOW)
        self.assertIn('narbe-v0.1.0-beta.3-release', WORKFLOW)

    def test_package_write_job_waits_for_unprivileged_gate_and_both_builds(self):
        gate_job=WORKFLOW.split('  source-gate:',1)[1].split('  build:',1)[0]
        self.assertIn('contents: read',gate_job)
        self.assertNotIn('packages: write',gate_job)
        publish=WORKFLOW.split('  publish:',1)[1].split('  verify-published:',1)[0]
        self.assertIn('needs: [source-gate, build]',publish)
        self.assertIn("if: needs.source-gate.outputs.approved == 'true'",publish)
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
        self.assertIn('v0.1.0-beta.2@sha256:e9817422',WORKFLOW)


if __name__ == '__main__':
    unittest.main()

