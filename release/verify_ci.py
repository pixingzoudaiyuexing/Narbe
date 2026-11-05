#!/usr/bin/env python3
"""Read-only exact-source CI gate for the Owner-authorized beta.5 release.

Only the canonical repository's push CI on the release branch is eligible.
The newest matching run, all its jobs, and all their steps must succeed.
"""
import argparse
import json
import math
import os
from pathlib import Path
import re
import time
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = 'pixingzoudaiyuexing/Narbe'
WORKFLOW_PATH = '.github/workflows/ci.yml'
BRANCH = 'codex/narbe-phase1'
REQUIRED_JOBS = frozenset({'verify', 'arm64-runtime'})
API_PREFIX = '/repos/' + REPOSITORY + '/'
MAX_RESPONSE_BYTES = 5 * 1024 * 1024


class GateError(ValueError):
    """A CI result cannot safely authorize publication."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Never forward the token to a redirect destination, even on GitHub.
        return None


class GitHubAPI:
    def __init__(self, token):
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect())

    def get(self, path, params, timeout):
        if (not path.startswith(API_PREFIX + 'actions/') or
                not re.fullmatch(r'/[A-Za-z0-9_./-]+', path) or
                any(part in {'.', '..'} for part in path.split('/'))):
            raise GateError('Refusing a non-canonical GitHub API request path')
        url = 'https://api.github.com' + path
        if params:
            url += '?' + urllib.parse.urlencode(params)
        request = urllib.request.Request(url, method='GET', headers={
            'Accept': 'application/vnd.github+json',
            'Authorization': 'Bearer ' + self.token,
            'X-GitHub-Api-Version': '2022-11-28',
            'User-Agent': 'Narbe-beta5-exact-source-ci-gate',
        })
        try:
            with self.opener.open(request, timeout=min(30, timeout)) as response:
                raw = response.read(MAX_RESPONSE_BYTES + 1)
            if len(raw) > MAX_RESPONSE_BYTES:
                raise GateError('GitHub API response exceeds the size limit')
            value = json.loads(raw.decode('utf-8'))
        except urllib.error.HTTPError as error:
            raise GateError('GitHub API HTTP ' + str(error.code)) from None
        except (urllib.error.URLError, OSError, TimeoutError):
            raise GateError('GitHub API request failed') from None
        except (ValueError, UnicodeError):
            raise GateError('Invalid GitHub API JSON') from None
        if not isinstance(value, dict):
            raise GateError('Invalid GitHub API response object')
        return value


def positive_integer(value):
    return isinstance(value, int) and not isinstance(value, bool) and value > 0


def matching_run(value, sha):
    if not isinstance(value, dict):
        raise GateError('Invalid workflow run record')
    path = value.get('path')
    return (value.get('head_sha') == sha and
            value.get('head_branch') == BRANCH and
            value.get('event') == 'push' and
            isinstance(path, str) and
            re.fullmatch(re.escape(WORKFLOW_PATH) + r'(?:@[A-Za-z0-9_./-]+)?', path) is not None)


def validate_run(value, sha, run_id):
    if not matching_run(value, sha) or value.get('id') != run_id:
        raise GateError('Selected workflow run identity changed')
    if not positive_integer(run_id) or not positive_integer(value.get('run_attempt')):
        raise GateError('Invalid workflow run or attempt identity')
    expected = f'https://github.com/{REPOSITORY}/actions/runs/{run_id}'
    if value.get('html_url') != expected:
        raise GateError('Selected workflow run URL is not canonical')


def pages(api, path, params, key, remaining):
    result = []
    expected_total = None
    # GitHub's filtered workflow-run API returns at most 1000 results. Refuse
    # inconsistent/truncated pagination rather than silently omitting newer CI.
    for page in range(1, 101):
        response = api.get(path, dict(params, per_page=100, page=page), remaining())
        total, items = response.get('total_count'), response.get(key)
        if (not isinstance(total, int) or isinstance(total, bool) or total < 0 or
                not isinstance(items, list) or len(items) > 100):
            raise GateError('Invalid GitHub API pagination response')
        if expected_total is None:
            expected_total = total
        elif total != expected_total:
            raise GateError('GitHub API collection changed during pagination')
        result.extend(items)
        if len(result) == total:
            return result
        if len(result) > total or len(items) != 100:
            raise GateError('Incomplete GitHub API pagination')
    raise GateError('GitHub API pagination exceeds the safety limit')


def validate_jobs(jobs, run_id, attempt, sha):
    evidence = []
    names = set()
    ids = set()
    for job in jobs:
        if not isinstance(job, dict):
            raise GateError('Invalid CI job record')
        name, job_id = job.get('name'), job.get('id')
        if (not isinstance(name, str) or not name or name in names or
                not positive_integer(job_id) or job_id in ids or
                job.get('run_id') != run_id or job.get('run_attempt') != attempt or
                job.get('head_sha') != sha):
            raise GateError('CI job source, attempt, or identity mismatch')
        names.add(name)
        ids.add(job_id)
        if job.get('status') != 'completed' or job.get('conclusion') != 'success':
            raise GateError('CI job is not successful: ' + name)
        url = f'https://github.com/{REPOSITORY}/actions/runs/{run_id}/job/{job_id}'
        if job.get('html_url') != url:
            raise GateError('CI job URL is not canonical')
        steps = job.get('steps')
        if not isinstance(steps, list) or not steps:
            raise GateError('CI job has no verifiable steps: ' + name)
        numbers = set()
        for step in steps:
            if (not isinstance(step, dict) or not isinstance(step.get('name'), str) or
                    not step.get('name') or not positive_integer(step.get('number')) or
                    step['number'] in numbers or step.get('status') != 'completed' or
                    step.get('conclusion') != 'success'):
                raise GateError('CI job contains an unsuccessful or invalid step: ' + name)
            numbers.add(step['number'])
        evidence.append(dict(name=name, conclusion='success', stepCount=len(steps), url=url))
    if not REQUIRED_JOBS.issubset(names):
        raise GateError('CI is missing required verify or arm64-runtime jobs')
    return evidence


def verify(sha, timeout=900, token=None, repository=None, api=None,
           clock=time.monotonic, sleep=time.sleep):
    token = os.environ.get('GH_TOKEN', '') if token is None else token
    repository = os.environ.get('GITHUB_REPOSITORY', '') if repository is None else repository
    if not re.fullmatch(r'[0-9a-f]{40}', sha or ''):
        raise GateError('Source SHA must be a complete lowercase 40-character commit SHA')
    if repository != REPOSITORY:
        raise GateError('GITHUB_REPOSITORY must identify the canonical Narbe repository')
    if not token or any(ord(char) < 33 or ord(char) > 126 for char in token):
        raise GateError('A valid GH_TOKEN is required for read-only CI verification')
    if (not isinstance(timeout, (int, float)) or isinstance(timeout, bool) or
            not math.isfinite(timeout) or timeout <= 0):
        raise GateError('Timeout must be a positive finite duration')
    api = api or GitHubAPI(token)
    deadline = clock() + timeout

    def remaining():
        left = deadline - clock()
        if left <= 0:
            raise GateError('Timed out waiting for exact-source push CI')
        return left

    while True:
        candidates = pages(api, API_PREFIX + 'actions/workflows/ci.yml/runs',
                           dict(head_sha=sha, branch=BRANCH, event='push', exclude_pull_requests='true'),
                           'workflow_runs', remaining)
        matches = [item for item in candidates if matching_run(item, sha)]
        for value in matches:
            if not positive_integer(value.get('id')):
                raise GateError('Invalid workflow run identity')
        if matches:
            # Do not query only successful runs: a newer failed or pending run
            # must never fall back to an older successful run for this source.
            latest = max(matches, key=lambda item: item['id'])
            run_id = latest['id']
            path = API_PREFIX + 'actions/runs/' + str(run_id)
            current = api.get(path, {}, remaining())
            validate_run(current, sha, run_id)
            status = current.get('status')
            if status == 'completed':
                conclusion = current.get('conclusion')
                if conclusion != 'success':
                    safe_conclusion = conclusion if conclusion in {
                        'failure', 'cancelled', 'skipped', 'neutral', 'timed_out',
                        'action_required', 'stale', 'startup_failure',
                    } else 'invalid conclusion'
                    raise GateError('Exact-source CI did not succeed: ' + safe_conclusion)
                attempt = current['run_attempt']
                jobs = pages(api, path + '/jobs', dict(filter='latest'), 'jobs', remaining)
                evidence = validate_jobs(jobs, run_id, attempt, sha)
                final = api.get(path, {}, remaining())
                validate_run(final, sha, run_id)
                if (final.get('run_attempt') != attempt or final.get('status') != 'completed' or
                        final.get('conclusion') != 'success'):
                    raise GateError('CI run changed while its jobs were being verified')
                return dict(sourceSha=sha, conclusion='success', workflowUrl=current['html_url'],
                            runId=run_id, workflowPath=WORKFLOW_PATH, jobs=evidence)
            if status not in {'queued', 'in_progress', 'waiting', 'pending', 'requested'}:
                raise GateError('Exact-source CI has an unsupported state')
            if current.get('conclusion') is not None:
                raise GateError('Pending CI unexpectedly has a terminal conclusion')
        sleep(min(20, remaining()))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--sha', required=True)
    parser.add_argument('--timeout', type=float, default=900)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    try:
        record = verify(args.sha, timeout=args.timeout)
    except GateError as error:
        raise SystemExit('Release CI gate rejected: ' + str(error)) from None
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(record, indent=2))


if __name__ == '__main__':
    main()
