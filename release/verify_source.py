#!/usr/bin/env python3
"""Verify two independently approved Git commits before executing release code."""
import argparse
import json
import re
import subprocess
from pathlib import Path

APPROVED_APPLICATION = 'ebc933f2842be343ffcb6eefd240cdab54ef1b4d'
VERSION = 'v0.1.0-beta.4'
# Exact paths, not directories/globs. These changes need separate formal review.
RELEASE_FILES = frozenset({
    '.github/workflows/release.yml', '.github/workflows/ci.yml',
    'release/VERSION', 'release/publish.sh', 'release/bundle.py',
    'release/test_bundle.py', 'release/verify_source.py', 'release/test_verify_source.py',
    'release/README.md', 'release/NOTES.md', 'docs/BETA4_RELEASE_REVIEW.md',
})
POST_REVIEW_TEXT = frozenset({'release/README.md', 'release/NOTES.md'})


def release_files(sha):
    # Review exceptions are exact regular files, never symlinks, submodules,
    # directories, or files whose execute bit changes their interpretation.
    for path in RELEASE_FILES:
        mode = '100755' if path == 'release/publish.sh' else '100644'
        if not git('ls-tree', sha, '--', path).startswith(mode + ' blob '):
            raise ValueError('Release file must retain its regular file type/mode: ' + path)
    for path in POST_REVIEW_TEXT:
        raw = subprocess.check_output(['git', 'show', sha + ':' + path])
        text = raw.decode('utf-8')
        if any(ord(c) < 32 and c not in '\t\r\n' for c in text):
            raise ValueError('Release notes must remain ordinary UTF-8 text: ' + path)


def git(*args):
    return subprocess.check_output(['git', *args], stderr=subprocess.PIPE).decode().strip()


def commit(value):
    if not re.fullmatch(r'[0-9a-f]{40}', value or ''):
        raise ValueError('Review SHA must be a complete lowercase 40-character commit SHA')
    if git('rev-parse', '--verify', value + '^{commit}') != value:
        raise ValueError('Review SHA does not identify a commit directly')
    return value


def ancestor(base, head):
    if subprocess.run(['git', 'merge-base', '--is-ancestor', base, head],
                      stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0:
        raise ValueError('Approved commit is not an ancestor of the release')


def changed(base, head):
    raw = subprocess.check_output(['git', 'diff', '--no-ext-diff', '--no-renames',
                                   '--name-only', '-z', base, head, '--'])
    return set(raw.decode().rstrip('\0').split('\0')) if raw else set()


def verify(app, release, head):
    app, release, head = commit(app), commit(release), commit(head)
    if app != APPROVED_APPLICATION:
        raise ValueError('Application SHA is not the formally approved beta.4 baseline')
    ancestor(app, release)
    ancestor(release, head)
    unapproved = changed(app, release) - RELEASE_FILES
    if unapproved:
        raise ValueError('Unapproved application or non-allowlisted changes: ' + ', '.join(sorted(unapproved)))
    unapproved = changed(release, head) - POST_REVIEW_TEXT
    if unapproved:
        raise ValueError('Unreviewed executable or non-allowlisted changes: ' + ', '.join(sorted(unapproved)))
    for sha in {release, head}:
        release_files(sha)
        if git('show', sha + ':release/VERSION') != VERSION:
            raise ValueError('Release version does not match beta.4')
    if git('rev-parse', 'HEAD') != head:
        raise ValueError('Checked-out HEAD differs from the release commit')
    if subprocess.run(['git', 'diff', '--quiet', 'HEAD', '--']).returncode:
        raise ValueError('Tracked working tree differs from the reviewed release')
    # CI downloads artifacts only under ignored .local/. Reject source injection.
    # Include ignored inputs too: an ignored asset can still enter go:embed.
    # Restrict enumeration to protected paths so node_modules/.local stay out.
    raw = subprocess.check_output(['git', 'ls-files', '--others', '-z', '--',
                                   'cmd', 'internal', 'scripts', 'release', '.github',
                                   'Dockerfile', '.dockerignore', 'compose.yaml',
                                   'go.mod', 'go.sum', 'package.json', 'package-lock.json'])
    untracked = raw.decode().rstrip('\0').split('\0') if raw else []
    for path in untracked:
        if path.startswith(('cmd/', 'internal/', 'scripts/', 'release/', '.github/')) or path in {
            'Dockerfile', '.dockerignore', 'compose.yaml', 'go.mod', 'go.sum', 'package.json', 'package-lock.json',
        }:
            raise ValueError('Untracked executable/build input: ' + path)
    return {'applicationReviewedSha': app, 'releaseReviewedSha': release,
            'sourceSha': head, 'version': VERSION,
            'postReviewTextChanges': sorted(changed(release, head))}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--application', required=True)
    parser.add_argument('--release', required=True)
    parser.add_argument('--head', required=True)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    try:
        record = verify(args.application, args.release, args.head)
    except (ValueError, subprocess.CalledProcessError, UnicodeError) as error:
        raise SystemExit('Release source gate rejected: ' + str(error)) from None
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(record, indent=2))


if __name__ == '__main__':
    main()
