#!/usr/bin/env python3
"""Publish retained release builds, resuming only matching images and assets."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys


def capture(*args):
    return subprocess.check_output(args, text=True).strip()


def api(*args):
    return capture('gh', 'api', *args)


def api_list(path):
    pages = json.loads(api('--paginate', '--slurp', path + '?per_page=100'))
    return [item for page in pages for item in page]


def image_digest(reference, *, allow_missing=False):
    result = subprocess.run(['skopeo', 'inspect', '--raw', reference], capture_output=True)
    if result.returncode:
        error = result.stderr.decode(errors='replace').strip()
        if allow_missing and re.search(r': (?:manifest unknown|name unknown)\b', error, re.I):
            return None
        raise SystemExit(error or f'Could not inspect {reference}')
    return 'sha256:' + hashlib.sha256(result.stdout).hexdigest()


def verify_remote_tag(repo, tag, commit):
    target = json.loads(api(f'{repo}/git/ref/tags/{tag}'))['object']
    while target['type'] == 'tag':
        target = json.loads(api(f'{repo}/git/tags/{target["sha"]}'))['object']
    if target['type'] != 'commit' or target['sha'] != commit:
        raise SystemExit(f'The remote tag {tag} does not point to {commit}')


def image_missing(remote, expected, *, published):
    actual = image_digest(remote, allow_missing=True)
    if actual == expected:
        return False
    if actual is not None or published:
        raise SystemExit(f'{remote}: expected {expected}, found {actual or "no manifest"}')
    return True


def publish_images(repo, tag, commit, directory, destinations, *, published):
    # Catch existing conflicts before the first write. Another publisher can
    # still fill a destination while an earlier image is being copied.
    plans = []
    for name, destination in zip(('host', 'hack', 'standalone'), destinations, strict=True):
        source = f'oci-archive:{directory / (name + ".tar")}'
        expected = image_digest(source)
        remote = f'docker://{destination}'
        image_missing(remote, expected, published=published)
        plans.append((source, remote, expected))

    for source, remote, expected in plans:
        verify_remote_tag(repo, tag, commit)
        if not image_missing(remote, expected, published=published):
            print(f'{remote} already has {expected}')
            continue
        subprocess.run(['skopeo', 'copy', '--all', '--preserve-digests', source, remote], check=True)
        if image_digest(remote) != expected:
            raise SystemExit(f'{remote}: digest did not match {expected} after publication')
    verify_remote_tag(repo, tag, commit)
    return destinations[0].rsplit(':', 1)[0] + '@' + plans[0][2]


def stable_version(tag):
    if re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', tag):
        return tuple(map(int, tag[1:].split('.')))
    return None


def verify_assets(endpoint, expected, *, draft):
    present = set()
    for asset in api_list(endpoint):
        name = asset['name']
        if name not in expected:
            raise SystemExit(f'Unexpected release asset {name}')
        if draft and asset['state'] == 'starter':
            # GitHub may leave an empty starter asset after an upload fails.
            releases_endpoint = endpoint.rsplit('/', 2)[0]
            api('--method', 'DELETE', f'{releases_endpoint}/assets/{asset["id"]}')
            continue
        if asset['state'] != 'uploaded' or asset.get('digest') != expected[name]:
            raise SystemExit(f'Release asset {name}: expected uploaded digest {expected[name]}, '
                             f'found {asset["state"]} with {asset.get("digest")}')
        present.add(name)
    return present


def stable_release(releases, tag):
    release = next((item for item in releases if item['tag_name'] == tag), None)
    if release is None or (release['draft'] and not (release.get('body') or '').strip()):
        raise SystemExit(f'Save the approved release notes in a draft for {tag} before publishing')
    if release['prerelease']:
        raise SystemExit(f'{tag} already has a prerelease')
    return release


def publish_downloads(repo, tag, commit, directory):
    version = stable_version(tag)
    if version is None:
        raise SystemExit(f'{tag} is not a stable release tag')
    verify_remote_tag(repo, tag, commit)

    subprocess.run(['sha256sum', '--check', 'SHA256SUMS'], cwd=directory, check=True)
    expected = {}
    for asset in sorted(directory.iterdir()):
        with asset.open('rb') as data:
            expected[asset.name] = 'sha256:' + hashlib.file_digest(data, 'sha256').hexdigest()

    releases = api_list(f'{repo}/releases')
    release = stable_release(releases, tag)

    endpoint = f'{repo}/releases/{release["id"]}'
    present = verify_assets(f'{endpoint}/assets', expected, draft=release['draft'])
    missing = expected.keys() - present
    if release['draft']:
        if missing:
            paths = [str(directory / name) for name in sorted(missing)]
            subprocess.run(['gh', 'release', 'upload', tag, *paths], check=True)
        missing = expected.keys() - verify_assets(f'{endpoint}/assets', expected, draft=False)
    if missing:
        raise SystemExit(f'{tag} is missing release assets: {", ".join(sorted(missing))}')

    latest = not any(not item['draft'] and not item['prerelease'] and
                     (stable_version(item['tag_name']) or (0, 0, 0)) > version for item in releases)
    if release['draft']:
        verify_remote_tag(repo, tag, commit)
        api('--method', 'PATCH', endpoint, '-F', 'draft=false', '-f', f'make_latest={str(latest).lower()}')
    return latest


def promote_latest(repo, tag, commit, source, destination):
    latest = api(f'{repo}/releases/latest', '--jq', '.tag_name')
    if latest != tag:
        print(f'Skipping {tag} because {latest} is the latest stable release')
        return
    verify_remote_tag(repo, tag, commit)
    subprocess.run(['skopeo', 'copy', '--all', '--preserve-digests',
                    f'docker://{source}', f'docker://{destination}'], check=True)


def main():
    command, tag, commit, *args = sys.argv[1:]
    repo = 'repos/' + os.environ['GITHUB_REPOSITORY']
    if command == 'check-stable-release':
        verify_remote_tag(repo, tag, commit)
        release = stable_release(api_list(f'{repo}/releases'), tag)
        output = 'approved_draft_tag=' + (tag if release['draft'] else '')
    elif command == 'images':
        directory, *destinations = args
        published = False
        if stable_version(tag) is not None:
            # A contents:read token can see published releases. The separate
            # stable check can also see drafts, which require push access.
            releases = api_list(f'{repo}/releases')
            if (any(item['tag_name'] == tag for item in releases)
                    or os.environ.get('APPROVED_DRAFT_TAG') != tag):
                published = not stable_release(releases, tag)['draft']
        digest = publish_images(repo, tag, commit, Path(directory), destinations, published=published)
        output = f'host_digest_ref={digest}'
    elif command == 'downloads':
        directory, = args
        latest = publish_downloads(repo, tag, commit, Path(directory))
        output = f'promote={str(latest).lower()}'
    elif command == 'promote':
        source, destination = args
        promote_latest(repo, tag, commit, source, destination)
        return
    else:
        raise SystemExit(f'Unknown publication command: {command}')
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        print(output, file=stream)


if __name__ == '__main__':
    main()
