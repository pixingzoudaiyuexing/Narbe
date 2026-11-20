#!/usr/bin/env python3
"""Verify two independently approved Git commits before executing release code."""
import argparse
import json
import re
import subprocess
from pathlib import Path

APPROVED_APPLICATION = '182e57759494c6fbcf6e3291d8259b524a9ea49a'
APPROVED_EVIDENCE = '800c5443cb5fb913e30fe71467642c6fdfa87b00'
VERSION = 'v0.1.0-beta.5'
APP_EVIDENCE_FILES = frozenset({
    'docs/BETA5_DELIVERY.md', 'docs/BETA5_SOURCES.md', 'docs/BETA5_EVIDENCE.json',
})
# Exact paths, not directories/globs. These changes need separate formal review.
RELEASE_FILES = frozenset({
    '.github/workflows/release.yml', '.github/workflows/ci.yml',
    'release/VERSION', 'release/publish.sh', 'release/bundle.py',
    'release/test_bundle.py', 'release/verify_source.py', 'release/test_verify_source.py',
    'release/README.md', 'release/NOTES.md', 'docs/BETA5_RELEASE_REVIEW.md',
})
POST_REVIEW_TEXT = frozenset({'release/README.md', 'release/NOTES.md'})

# Separate, explicit Owner-authorized path. These commits have passed automated
# validation; they are NOT relabelled as independently reviewed commits.
OWNER_MODE = 'owner-authorized-manual-review-waiver'
APPLICATION_BASELINE = '3216f1e9e5151338427c7ceee5fe0090fdebf1e4'
INTEGRATED_PR_HEAD = 'ec66fd4e90f38a1940b8cdf662868bd1a5a0a70e'
OWNER_RELEASE_FILES = frozenset({
    '.github/workflows/release.yml', '.github/workflows/ci.yml',
    'release/VERSION', 'release/publish.sh', 'release/bundle.py',
    'release/test_bundle.py', 'release/verify_source.py', 'release/test_verify_source.py',
    'release/verify_ci.py', 'release/test_verify_ci.py',
    'release/OWNER_AUTHORIZATION.json', 'release/README.md', 'release/NOTES.md',
    'README.md', 'docs/REVIEW.md', 'docs/BETA5_OWNER_RELEASE.md',
})


def evidence_files(sha):
    for path in APP_EVIDENCE_FILES:
        if not git('ls-tree', sha, '--', path).startswith('100644 blob '):
            raise ValueError('Evidence must retain its regular file type/mode: ' + path)
        text = subprocess.check_output(['git', 'show', sha + ':' + path]).decode('utf-8')
        if any(ord(c) < 32 and c not in '\t\r\n' for c in text):
            raise ValueError('Evidence must remain ordinary UTF-8 text: ' + path)
        if path.endswith('.json'):
            json.loads(text)


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


def clean_build_inputs(head):
    if git('rev-parse', 'HEAD') != head:
        raise ValueError('Checked-out HEAD differs from the release commit')
    if subprocess.run(['git', 'diff', '--quiet', 'HEAD', '--']).returncode:
        raise ValueError('Tracked working tree differs from the authorized release')
    raw = subprocess.check_output(['git', 'ls-files', '--others', '-z', '--',
                                   'cmd', 'internal', 'scripts', 'release', '.github',
                                   'Dockerfile', 'Dockerfile.probe', '.dockerignore', 'compose.yaml', 'candidate', 'deploy',
                                   'go.mod', 'go.sum', 'package.json', 'package-lock.json'])
    if raw:
        raise ValueError('Untracked executable/build input: ' +
                         ', '.join(raw.decode().rstrip('\0').split('\0')))


def verify_owner(authorized, head):
    authorized, head = commit(authorized), commit(head)
    if authorized != head:
        raise ValueError('Owner authorization requires the exact source SHA; no later changes allowed')
    baseline, integrated = commit(APPLICATION_BASELINE), commit(INTEGRATED_PR_HEAD)
    ancestor(baseline, integrated)
    ancestor(integrated, head)
    unapproved = changed(integrated, head) - OWNER_RELEASE_FILES
    if unapproved:
        raise ValueError('Unauthorized application/build input changes: ' + ', '.join(sorted(unapproved)))
    for path in OWNER_RELEASE_FILES:
        mode = '100755' if path == 'release/publish.sh' else '100644'
        if not git('ls-tree', head, '--', path).startswith(mode + ' blob '):
            raise ValueError('Owner release file must retain its regular file type/mode: ' + path)
    for path in ('release/OWNER_AUTHORIZATION.json', 'release/README.md', 'release/NOTES.md'):
        text = subprocess.check_output(['git', 'show', head + ':' + path]).decode('utf-8')
        if any(ord(c) < 32 and c not in '\t\r\n' for c in text):
            raise ValueError('Owner authorization and release notes must be ordinary UTF-8 text')
    authorization = json.loads(git('show', head + ':release/OWNER_AUTHORIZATION.json'))
    expected = {'mode': OWNER_MODE, 'authorizedBy': 'Owner', 'authorizationDate': '2026-11-05',
                'manualIndependentReview': 'waived-by-owner',
                'applicationBaselineSha': baseline, 'integratedPRHead': integrated}
    if any(authorization.get(k) != v for k, v in expected.items()):
        raise ValueError('Explicit Owner manual-review waiver is missing or inconsistent')
    if 'applicationReviewedSha' in authorization or 'releaseReviewedSha' in authorization:
        raise ValueError('Owner waiver must not impersonate an independent review')
    if git('show', head + ':release/VERSION') != VERSION:
        raise ValueError('Release version does not match beta.5')
    clean_build_inputs(head)
    return {'releaseMode': OWNER_MODE, 'sourceSha': head, 'authorizedSourceSha': authorized,
            'applicationBaselineSha': baseline, 'integratedPRHead': integrated,
            'ownerAuthorization': authorization, 'version': VERSION,
            'releaseConfigurationChanges': sorted(changed(integrated, head))}


def verify(app, release, head):
    app, release, head = commit(app), commit(release), commit(head)
    evidence = commit(APPROVED_EVIDENCE)
    if app != APPROVED_APPLICATION:
        raise ValueError('Application SHA is not the formally approved beta.5 baseline')
    ancestor(app, evidence)
    ancestor(evidence, release)
    ancestor(release, head)
    unapproved = changed(app, evidence) - APP_EVIDENCE_FILES
    if unapproved:
        raise ValueError('Unapproved delivery evidence changes: ' + ', '.join(sorted(unapproved)))
    unapproved = changed(evidence, release) - RELEASE_FILES
    if unapproved:
        raise ValueError('Unapproved application or non-allowlisted changes: ' + ', '.join(sorted(unapproved)))
    unapproved = changed(release, head) - POST_REVIEW_TEXT
    if unapproved:
        raise ValueError('Unreviewed executable or non-allowlisted changes: ' + ', '.join(sorted(unapproved)))
    for sha in {evidence, release, head}:
        evidence_files(sha)
    for sha in {release, head}:
        release_files(sha)
        if git('show', sha + ':release/VERSION') != VERSION:
            raise ValueError('Release version does not match beta.5')
    clean_build_inputs(head)
    return {'applicationReviewedSha': app, 'releaseReviewedSha': release,
            'deliveryEvidenceSha': evidence,
            'sourceSha': head, 'version': VERSION,
            'applicationEvidenceChanges': sorted(changed(app, evidence)),
            'releaseConfigurationChanges': sorted(changed(evidence, release)),
            'postReviewTextChanges': sorted(changed(release, head))}


# beta.6 is an explicit Owner test-release authorization, not a claim that the
# candidate received a new formal Independent Reviewer verdict.
TEST_VERSION='v0.1.0-beta.6'
TEST_MODE='owner-authorized-test-release'
TEST_APPLICATION_BASELINE='8d36215df29d2c1bb854c61afcc00d4582c7fc8f'
TEST_RELEASE_FILES=frozenset({
 '.github/workflows/release.yml',
 'release/VERSION','release/publish.sh','release/verify_source.py','release/test_verify_source.py',
 'release/verify_ci.py','release/test_verify_ci.py','release/bundle.py','release/test_bundle.py',
 'release/OWNER_AUTHORIZATION.json','release/README.md','release/NOTES.md',
 'release/probe-compose.yaml','release/install-probe.sh','release/test_probe_install.py','release/verify_images.py','release/test_verify_images.py',
 'README.md','docs/BETA6_RELEASE.md','internal/web/assets/connectivity-ui.js',
})
# The sole application-file exception is a literal deployment-instructions
# update. Populated with the exact reviewed old/new text; arbitrary JS fails.
TEST_UI_REPLACEMENTS=[['使用本次构建的候选镜像 <span class="mono">narbe-probe:cn-candidate</span>（尚未正式发布）。管理端必须可通过可信 HTTPS 访问。', '使用正式测试版 <span class="mono">v0.1.0-beta.6</span> 探针镜像，安装包已固定不可变 Digest。管理端必须可通过可信 HTTPS 访问。'], ['在探针机器的候选源码目录，按部署文档将此凭据保存至 deploy/probe-registration-token.txt，然后运行下列命令。不要把云厂商 Token 填入探针。', '从 GitHub 的 Narbe v0.1.0-beta.6 Release 下载并解压正式安装包，在探针机器的解压目录运行下列命令，按提示输入管理端 HTTPS 地址及上方一次性凭据。不要把云厂商 Token 填入探针。'], ['export NARBE_PROBE_CONTROL_URL=https://narbe.example.com\ndocker compose -f deploy/probe-compose.yaml up -d --build\n\ndocker compose -f deploy/probe-compose.yaml logs --tail=50 probe', './install-probe.sh\n\n./install-probe.sh status\n./install-probe.sh logs'], ['替换 HTTPS 域名。日志不会输出云密钥；在线及出口确认以后台状态为准。', '安装器拉取已发布镜像，不在探针机器编译源码。已有配置和身份保留；在线及实际出口以后台状态为准。']]

def verify_test_release(authorized,head):
 authorized,head=commit(authorized),commit(head)
 if authorized!=head:raise ValueError('Owner authorization requires the exact source SHA; no later changes allowed')
 baseline=commit(TEST_APPLICATION_BASELINE);ancestor(baseline,head)
 differences=changed(baseline,head)
 unapproved=differences-TEST_RELEASE_FILES
 if unapproved:raise ValueError('Unauthorized application/build input changes: '+', '.join(sorted(unapproved)))
 for path in TEST_RELEASE_FILES:
  mode='100755' if path in {'release/publish.sh','release/install-probe.sh'} else '100644'
  if not git('ls-tree',head,'--',path).startswith(mode+' blob '):raise ValueError('Test release file must retain its regular file type/mode: '+path)
  raw=subprocess.check_output(['git','show',head+':'+path]);text=raw.decode('utf-8')
  if any(ord(c)<32 and c not in '\t\r\n' for c in text):raise ValueError('Test release files must be ordinary UTF-8 text: '+path)
 ui='internal/web/assets/connectivity-ui.js'
 expected_ui=subprocess.check_output(['git','show',baseline+':'+ui]).decode('utf-8')
 for old,new in TEST_UI_REPLACEMENTS:
  if expected_ui.count(old)!=1:raise ValueError('UI deployment-copy baseline changed')
  expected_ui=expected_ui.replace(old,new,1)
 if subprocess.check_output(['git','show',head+':'+ui]).decode('utf-8')!=expected_ui:raise ValueError('Unauthorized frontend change beyond deployment instructions')
 authorization=json.loads(git('show',head+':release/OWNER_AUTHORIZATION.json'))
 expected={'mode':TEST_MODE,'authorizedBy':'Owner','authorizationDate':'2026-11-20',
  'authorizationText':'发布新的测试版本','manualIndependentReview':'not-performed-for-this-candidate',
  'applicationBaselineSha':baseline,'version':TEST_VERSION}
 if any(authorization.get(k)!=v for k,v in expected.items()):raise ValueError('Explicit current Owner test-release authorization is missing or inconsistent')
 forbidden={'applicationReviewedSha','releaseReviewedSha','independentReview','deliveryEvidenceSha'}
 if forbidden.intersection(authorization):raise ValueError('Owner test release must not impersonate independent review')
 if git('show',head+':release/VERSION')!=TEST_VERSION:raise ValueError('Test release version does not match beta.6')
 clean_build_inputs(head)
 return {'releaseMode':TEST_MODE,'sourceSha':head,'authorizedSourceSha':authorized,
  'applicationBaselineSha':baseline,'ownerAuthorization':authorization,'version':TEST_VERSION,
  'releaseConfigurationChanges':sorted(differences)}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--application')
    parser.add_argument('--release')
    parser.add_argument('--owner-authorized-source')
    parser.add_argument('--owner-authorized-test-source')
    parser.add_argument('--head', required=True)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    try:
        if args.owner_authorized_test_source is not None:
            if args.application is not None or args.release is not None or args.owner_authorized_source is not None:
                raise ValueError('Test release, historical waiver and review paths cannot be mixed')
            record=verify_test_release(args.owner_authorized_test_source,args.head)
        elif args.owner_authorized_source is not None:
            if args.application is not None or args.release is not None:
                raise ValueError('Owner waiver and independent-review paths cannot be mixed')
            record = verify_owner(args.owner_authorized_source, args.head)
        else:
            record = verify(args.application, args.release, args.head)
    except (ValueError, subprocess.CalledProcessError, UnicodeError) as error:
        raise SystemExit('Release source gate rejected: ' + str(error)) from None
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(record, indent=2) + '\n')
    print(json.dumps(record, indent=2))


if __name__ == '__main__':
    main()
