#!/usr/bin/env python3
"""Fail-closed tests for exact-source GitHub Actions release evidence."""
import copy
import importlib.util
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock
import urllib.error

ROOT = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location('verify_ci', ROOT / 'verify_ci.py')
ci = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(ci)
SHA = 'a' * 40
REPO = 'pixingzoudaiyuexing/Narbe'


def run(run_id=42, **overrides):
    value = dict(id=run_id, path='.github/workflows/ci.yml', head_sha=SHA,
                 head_branch='codex/narbe-phase1', event='push',
                 status='completed', conclusion='success', run_attempt=1,
                 html_url=f'https://github.com/{REPO}/actions/runs/{run_id}')
    value.update(overrides)
    return value


def job(name, job_id=1, run_id=42, **overrides):
    value = dict(id=job_id, run_id=run_id, run_attempt=1, head_sha=SHA,
                 name=name, status='completed', conclusion='success',
                 html_url=f'https://github.com/{REPO}/actions/runs/{run_id}/job/{job_id}',
                 steps=[dict(name='Actual checks', number=1, status='completed', conclusion='success')])
    value.update(overrides)
    return value


class Clock:
    def __init__(self):
        self.now = 0
        self.sleeps = []

    def __call__(self):
        return self.now

    def sleep(self, seconds):
        self.sleeps.append(seconds)
        self.now += seconds


class API:
    def __init__(self, snapshots=None, jobs=None):
        self.snapshots = snapshots or [[run()]]
        self.jobs = jobs if jobs is not None else [job('verify'), job('arm64-runtime', 2)]
        self.calls = []
        self.polls = 0
        self.current = None

    def get(self, path, params, timeout):
        self.calls.append((path, params, timeout))
        page = params.get('page', 1)
        if path.endswith('/workflows/ci.yml/runs'):
            if page == 1:
                self.current = self.snapshots[min(self.polls, len(self.snapshots)-1)]
                self.polls += 1
            items = self.current[(page-1)*100:page*100]
            return dict(total_count=len(self.current), workflow_runs=copy.deepcopy(items))
        if path.endswith('/jobs'):
            return dict(total_count=len(self.jobs), jobs=copy.deepcopy(self.jobs[(page-1)*100:page*100]))
        for value in self.current:
            if path.endswith('/' + str(value['id'])):
                return copy.deepcopy(value)
        raise AssertionError('Unexpected API path: ' + path)


class ExactCI(unittest.TestCase):
    def verify(self, api=None, timeout=40, **kwargs):
        clock = Clock()
        self.clock = clock
        return ci.verify(SHA, timeout=timeout, token='test-token', repository=REPO,
                         api=api or API(), clock=clock, sleep=clock.sleep, **kwargs)

    def test_success_returns_fixed_source_and_all_jobs(self):
        api = API(jobs=[job('verify'), job('arm64-runtime', 2), job('extra-check', 3)])
        evidence = self.verify(api)
        self.assertEqual(evidence, dict(sourceSha=SHA, conclusion='success', runId=42,
                         workflowPath='.github/workflows/ci.yml',
                         workflowUrl=f'https://github.com/{REPO}/actions/runs/42',
                         jobs=[dict(name=name, conclusion='success', stepCount=1,
                               url=f'https://github.com/{REPO}/actions/runs/42/job/{i}')
                               for name, i in [('verify', 1), ('arm64-runtime', 2), ('extra-check', 3)]]))
        params = api.calls[0][1]
        self.assertEqual(params['head_sha'], SHA)
        self.assertEqual(params['branch'], 'codex/narbe-phase1')
        self.assertEqual(params['event'], 'push')

    def test_accepts_official_workflow_path_with_ref_suffix(self):
        self.assertEqual(self.verify(API([[run(path='.github/workflows/ci.yml@codex/narbe-phase1')]]))['runId'], 42)

    def test_missing_token_invalid_repository_and_sha_rejected_without_http(self):
        api = API()
        for values in [dict(token=''), dict(token='bad\ntoken'), dict(repository='other/Narbe'),
                       dict(sha='main'), dict(sha='A'*40), dict(sha='a'*39), dict(timeout=0)]:
            with self.subTest(values=values):
                args = dict(sha=SHA, timeout=1, token='test-token', repository=REPO, api=api)
                args.update(values)
                with self.assertRaises(ci.GateError):
                    ci.verify(**args)
        self.assertEqual(api.calls, [])

    def test_wrong_sha_branch_workflow_or_event_never_authorizes(self):
        for change in [dict(head_sha='b'*40), dict(head_branch='main'), dict(event='pull_request'),
                       dict(path='.github/workflows/release.yml'), dict(path='ci.yml'),
                       dict(path='.github/workflows/ci.yml/extra'), dict(path='.github/workflows/ci.yml@')]:
            with self.subTest(change=change), self.assertRaisesRegex(ci.GateError, 'Timed out'):
                self.verify(API([[run(**change)]]), timeout=1)

    def test_new_failed_run_does_not_reuse_older_success(self):
        api = API([[run(41), run(42, conclusion='failure')]])
        with self.assertRaisesRegex(ci.GateError, 'failure'):
            self.verify(api)
        self.assertEqual(self.clock.sleeps, [])

    def test_new_pending_run_waits_even_if_older_success_exists(self):
        api = API([[run(41), run(42, status='queued', conclusion=None)], [run(41), run(42)]])
        self.assertEqual(self.verify(api)['runId'], 42)
        self.assertEqual(self.clock.sleeps, [20])

    def test_no_run_then_in_progress_then_success(self):
        api = API([[], [run(status='in_progress', conclusion=None)], [run()]])
        self.assertEqual(self.verify(api, timeout=60)['conclusion'], 'success')
        self.assertEqual(self.clock.sleeps, [20, 20])

    def test_timeout_never_outputs_success(self):
        with self.assertRaisesRegex(ci.GateError, 'Timed out'):
            self.verify(API([[run(status='queued', conclusion=None)]]), timeout=21)
        self.assertEqual(self.clock.sleeps, [20, 1])

    def test_run_terminal_failure_cancel_or_skip_is_immediate_failure(self):
        for result in ['failure', 'cancelled', 'skipped', 'neutral', 'timed_out', None]:
            with self.subTest(result=result), self.assertRaises(ci.GateError):
                self.verify(API([[run(conclusion=result)]]))
            self.assertEqual(self.clock.sleeps, [])

    def test_required_jobs_cannot_be_missing_or_duplicated(self):
        for jobs in [[], [job('verify')], [job('arm64-runtime')],
                     [job('verify'), job('arm64-runtime', 2), job('verify', 3)]]:
            with self.subTest(jobs=jobs), self.assertRaises(ci.GateError):
                self.verify(API(jobs=jobs))

    def test_any_failed_or_skipped_job_including_extra_job_rejects(self):
        for state in ['failure', 'skipped', 'cancelled', None]:
            with self.subTest(state=state), self.assertRaises(ci.GateError):
                self.verify(API(jobs=[job('verify'), job('arm64-runtime', 2), job('other', 3, conclusion=state)]))

    def test_every_step_must_be_completed_successful(self):
        for steps in [[], [dict(name='Check', number=1, status='completed', conclusion='skipped')],
                      [dict(name='Check', number=1, status='in_progress', conclusion=None)],
                      [dict(name='Check', number=1, status='completed', conclusion='failure')]]:
            with self.subTest(steps=steps), self.assertRaises(ci.GateError):
                self.verify(API(jobs=[job('verify', steps=steps), job('arm64-runtime', 2)]))

    def test_job_source_attempt_identity_or_url_mismatch_rejected(self):
        for change in [dict(head_sha='b'*40), dict(run_id=43), dict(run_attempt=2),
                       dict(html_url='https://example.invalid/'), dict(status='queued')]:
            with self.subTest(change=change), self.assertRaises(ci.GateError):
                self.verify(API(jobs=[job('verify', **change), job('arm64-runtime', 2)]))

    def test_pages_all_runs_and_jobs(self):
        runs = [run(i, head_branch='other') for i in range(1000, 1100)] + [run()]
        jobs = [job('check-'+str(i), i+10) for i in range(100)] + [job('verify', 200), job('arm64-runtime', 201)]
        api = API([runs], jobs)
        result = self.verify(api)
        self.assertEqual(len(result['jobs']), 102)
        self.assertTrue(any(path.endswith('/runs') and params.get('page') == 2 for path, params, _ in api.calls))
        self.assertTrue(any(path.endswith('/jobs') and params.get('page') == 2 for path, params, _ in api.calls))

    def test_http_error_and_malformed_response_fail_closed(self):
        for error in [ci.GateError('GitHub API HTTP 403'), ci.GateError('Invalid GitHub API JSON')]:
            api = API()
            api.get = mock.Mock(side_effect=error)
            with self.subTest(error=error), self.assertRaises(ci.GateError):
                self.verify(api)
        api = API()
        api.get = mock.Mock(return_value={'total_count': 2, 'workflow_runs': 'wrong'})
        with self.assertRaises(ci.GateError):
            self.verify(api)

    def test_refetched_run_cannot_change_source_or_success_attempt(self):
        api = API()
        original = api.get
        detail_calls = 0
        def changed(path, params, timeout):
            nonlocal detail_calls
            value = original(path, params, timeout)
            if path.endswith('/42'):
                detail_calls += 1
                if detail_calls == 2:
                    value['run_attempt'] = 2
            return value
        api.get = changed
        with self.assertRaisesRegex(ci.GateError, 'changed'):
            self.verify(api)


class HTTPPolicy(unittest.TestCase):
    def test_transport_is_fixed_get_headers_and_does_not_follow_redirects(self):
        response = mock.MagicMock()
        response.__enter__.return_value.read.return_value = b'{"workflow_runs":[],"total_count":0}'
        with mock.patch.object(ci.urllib.request, 'build_opener') as build:
            build.return_value.open.return_value = response
            client = ci.GitHubAPI('sensitive-token')
            client.get('/repos/'+REPO+'/actions/workflows/ci.yml/runs', {'head_sha': SHA}, 9)
            request = build.return_value.open.call_args.args[0]
            self.assertEqual(request.method, 'GET')
            self.assertTrue(request.full_url.startswith('https://api.github.com/repos/'+REPO+'/'))
            self.assertEqual(request.get_header('Authorization'), 'Bearer sensitive-token')
            self.assertEqual(build.return_value.open.call_args.kwargs['timeout'], 9)
            handlers = build.call_args.args
            self.assertTrue(any(isinstance(h, ci.NoRedirect) for h in handlers))
        handler = ci.NoRedirect()
        self.assertIsNone(handler.redirect_request(None, None, 302, '', {}, 'https://example.invalid'))

    def test_http_network_json_errors_are_sanitized(self):
        errors = [urllib.error.HTTPError('https://secret.invalid/sensitive-token', 403, 'sensitive-token', {}, None),
                  urllib.error.HTTPError('https://secret.invalid', 302, 'sensitive-token', {}, None),
                  urllib.error.URLError('sensitive-token')]
        for error in errors:
            with self.subTest(error=error), mock.patch.object(ci.urllib.request, 'build_opener') as build:
                build.return_value.open.side_effect = error
                with self.assertRaises(ci.GateError) as raised:
                    ci.GitHubAPI('sensitive-token').get('/repos/'+REPO+'/actions/runs/42', {}, 1)
                self.assertNotIn('sensitive-token', str(raised.exception))
                self.assertNotIn('secret.invalid', str(raised.exception))

    def test_invalid_or_oversized_api_payload_cannot_authorize(self):
        for payload in [b'{bad', b'[]', b'\xff', b'x' * (ci.MAX_RESPONSE_BYTES + 1)]:
            with self.subTest(size=len(payload)), mock.patch.object(ci.urllib.request, 'build_opener') as build:
                response = mock.MagicMock()
                response.__enter__.return_value.read.return_value = payload
                build.return_value.open.return_value = response
                with self.assertRaises(ci.GateError):
                    ci.GitHubAPI('token').get('/repos/'+REPO+'/actions/runs/42', {}, 1)

    def test_api_rejects_non_repository_or_external_request_paths(self):
        api = ci.GitHubAPI('token')
        for path in ['https://example.invalid', '//example.invalid', '/repos/other/narbe/actions/runs',
                     '/repos/'+REPO+'/../secrets', '/repos/'+REPO+'/actions/runs?token=bad']:
            with self.subTest(path=path), self.assertRaises(ci.GateError):
                api.get(path, {}, 1)

    def test_cli_success_writes_only_verified_record(self):
        record = dict(sourceSha=SHA, conclusion='success')
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'proof.json'
            with mock.patch.object(ci, 'verify', return_value=record) as verify, \
                    mock.patch('sys.argv', ['verify_ci.py', '--sha', SHA, '--timeout', '900', '--output', str(output)]), \
                    mock.patch('sys.stdout', new_callable=io.StringIO) as stdout:
                ci.main()
            verify.assert_called_once_with(SHA, timeout=900)
            self.assertEqual(json.loads(output.read_text()), record)
            self.assertEqual(json.loads(stdout.getvalue()), record)

    def test_cli_missing_token_has_no_evidence_output(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'proof.json'
            with mock.patch.dict(ci.os.environ, {'GITHUB_REPOSITORY': REPO}, clear=True), \
                    mock.patch('sys.argv', ['verify_ci.py', '--sha', SHA, '--timeout', '1', '--output', str(output)]):
                with self.assertRaises(SystemExit) as result:
                    ci.main()
            self.assertNotEqual(result.exception.code, 0)
            self.assertFalse(output.exists())


if __name__ == '__main__':
    unittest.main()
