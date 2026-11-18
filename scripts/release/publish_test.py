"""Release retry tests. All registry and GitHub calls stay inside the fixtures."""
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import publish


class ImagePublicationTest(unittest.TestCase):
    repo = 'repos/fixture/simple-host'
    commit = 'a' * 40

    def setUp(self):
        self.directory = Path('/fixture/images')
        self.destinations = ('registry.test/host:0.9.0', 'registry.test/host:hack-0.9.0',
                             'registry.test/hack:0.9.0')
        self.sources = {f'oci-archive:{self.directory / (name + ".tar")}': name.encode()
                        for name in ('host', 'hack', 'standalone')}
        self.registry = {}
        self.copied = []
        self.interrupt = True
        self.git_objects = {f'{self.repo}/git/ref/tags/v0.9.0': {'type': 'commit', 'sha': self.commit}}

    def api(self, path):
        if path not in self.git_objects:
            raise subprocess.CalledProcessError(1, ['gh', 'api', path], stderr='HTTP 404')
        return json.dumps({'object': self.git_objects[path]})

    def publish(self, tag='v0.9.0', *, published=False, skopeo=None):
        with (patch.object(publish, 'api', side_effect=self.api),
              patch.object(publish.subprocess, 'run', side_effect=skopeo or self.skopeo)):
            return publish.publish_images(self.repo, tag, self.commit, self.directory,
                                          self.destinations, published=published)

    def skopeo(self, args, **kwargs):
        command, *options = args[1:]
        if command == 'inspect':
            reference = options[-1]
            manifest = self.sources.get(reference, self.registry.get(reference))
            if manifest is None:
                return subprocess.CompletedProcess(args, 1, b'', b'registry error: manifest unknown')
            return subprocess.CompletedProcess(args, 0, manifest, b'')
        self.assertEqual(options[:-2], ['--all', '--preserve-digests'])
        source, destination = options[-2:]
        if self.interrupt and destination == 'docker://' + self.destinations[1]:
            self.interrupt = False
            raise subprocess.CalledProcessError(1, args)
        self.copied.append(destination)
        self.registry[destination] = self.sources[source]

    def test_retry_after_first_push(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertEqual(self.copied, ['docker://' + self.destinations[0]])
        digest = self.publish()
        self.assertEqual(digest, 'registry.test/host@sha256:' + hashlib.sha256(b'host').hexdigest())
        self.publish(published=True)
        self.assertEqual(self.copied, ['docker://' + dest for dest in self.destinations])

    def test_stable_job_checks_draft_and_verifies_published_images_on_retry(self):
        tag = 'v0.9.0'
        release = {'tag_name': tag, 'draft': True, 'prerelease': False, 'body': 'Release notes'}
        self.interrupt = False
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp, 'output')
            env = {'GITHUB_REPOSITORY': 'fixture/simple-host', 'GITHUB_OUTPUT': str(output)}
            with patch.dict(publish.os.environ, env), patch.object(publish, 'api', side_effect=self.api):
                for draft in (True, False):
                    release['draft'] = draft
                    with (patch.object(publish.sys, 'argv', ['publish.py', 'check-stable-release', tag, self.commit]),
                          patch.object(publish, 'api_list', return_value=[release])):
                        publish.main()
                    key, value = output.read_text().splitlines()[-1].split('=', 1)
                    self.assertEqual((key, value), ('approved_draft_tag', tag if draft else ''))
                    images = ['publish.py', 'images', tag, self.commit, str(self.directory), *self.destinations]
                    with (patch.dict(publish.os.environ, {'APPROVED_DRAFT_TAG': value}),
                          patch.object(publish.sys, 'argv', images),
                          patch.object(publish, 'api_list', return_value=[] if draft else [release]),
                          patch.object(publish.subprocess, 'run', side_effect=self.skopeo)):
                        publish.main()
                        if not draft:
                            del self.registry['docker://' + self.destinations[0]]
                            with self.assertRaisesRegex(SystemExit, 'no manifest'):
                                publish.main()
        self.assertEqual(self.copied, ['docker://' + dest for dest in self.destinations])

    def test_conflict_stops_before_any_push(self):
        self.registry['docker://' + self.destinations[2]] = b'another manifest'
        with self.assertRaisesRegex(SystemExit, 'expected sha256:.*found sha256:'):
            self.publish()
        self.assertEqual(self.copied, [])

    def test_destination_appears_during_first_copy(self):
        first, second, third = ('docker://' + dest for dest in self.destinations)
        self.interrupt = False
        for manifest in (b'hack', b'another manifest'):
            with self.subTest(manifest=manifest):
                self.registry.clear()
                self.copied.clear()

                def write_during_copy(args, **kwargs):
                    result = self.skopeo(args, **kwargs)
                    if args[1] == 'copy' and args[-1] == first:
                        self.registry[second] = manifest
                    return result

                if manifest == b'hack':
                    self.publish(skopeo=write_during_copy)
                    self.assertEqual(self.copied, [first, third])
                else:
                    with self.assertRaisesRegex(SystemExit, 'expected sha256:.*found sha256:'):
                        self.publish(skopeo=write_during_copy)
                    self.assertEqual(self.copied, [first])
                self.assertEqual(self.registry[second], manifest)

    def test_moved_or_deleted_tag_stops_before_any_push(self):
        for tag in ('v0.9.0', 'v0.9.0-rc.1'):
            ref = f'{self.repo}/git/ref/tags/{tag}'
            with self.subTest(tag=tag, change='moved'):
                self.git_objects[ref] = {'type': 'commit', 'sha': 'b' * 40}
                with self.assertRaisesRegex(SystemExit, 'does not point to'):
                    self.publish(tag)
                self.assertEqual(self.copied, [])
            with self.subTest(tag=tag, change='deleted'):
                del self.git_objects[ref]
                with self.assertRaises(subprocess.CalledProcessError) as error:
                    self.publish(tag)
                self.assertEqual(error.exception.cmd, ['gh', 'api', ref])
                self.assertEqual(self.copied, [])

    def test_annotated_tag_is_peeled_before_push(self):
        tag = 'v0.9.0-rc.1'
        annotation = 'b' * 40
        self.git_objects[f'{self.repo}/git/ref/tags/{tag}'] = {'type': 'tag', 'sha': annotation}
        target = f'{self.repo}/git/tags/{annotation}'
        self.git_objects[target] = {'type': 'commit', 'sha': 'c' * 40}
        with self.assertRaisesRegex(SystemExit, 'does not point to'):
            self.publish(tag)
        self.assertEqual(self.copied, [])
        self.git_objects[target]['sha'] = self.commit
        self.interrupt = False
        self.publish(tag)
        self.assertEqual(len(self.copied), 3)

    def test_tag_moved_during_first_or_final_copy_stops_publication(self):
        tag = 'v0.9.0'
        ref = f'{self.repo}/git/ref/tags/{tag}'
        self.interrupt = False
        for move_after in (1, 3):
            with self.subTest(move_after=move_after):
                self.git_objects[ref] = {'type': 'commit', 'sha': self.commit}
                self.registry.clear()
                self.copied.clear()

                def move_after_copy(args, **kwargs):
                    result = self.skopeo(args, **kwargs)
                    if args[1] == 'copy' and len(self.copied) == move_after:
                        self.git_objects[ref] = {'type': 'commit', 'sha': 'b' * 40}
                    return result

                with (patch.object(publish, 'api', side_effect=self.api),
                      patch.object(publish.subprocess, 'run', side_effect=move_after_copy),
                      self.assertRaisesRegex(SystemExit, 'does not point to')):
                    publish.publish_images(self.repo, tag, self.commit, self.directory,
                                           self.destinations, published=False)
                self.assertEqual(self.copied, ['docker://' + dest for dest in self.destinations[:move_after]])


class DownloadPublicationTest(unittest.TestCase):
    repo = 'repos/fixture/simple-host'
    commit = 'a' * 40

    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.directory = Path(temp.name)
        (self.directory / 'binary').write_bytes(b'fixture binary')
        (self.directory / 'SHA256SUMS').write_text(hashlib.sha256(b'fixture binary').hexdigest() + '  binary\n')
        self.releases = {}
        self.assets = {}
        self.interrupt = True
        self.latest = None
        self.uploads = []
        self.run = subprocess.run
        self.tag_commit = self.commit

    def draft(self, tag, notes='Approved release notes'):
        release_id = len(self.releases) + 1
        self.releases[tag] = {'id': release_id, 'tag_name': tag, 'draft': True,
                              'prerelease': False, 'body': notes}
        self.assets[release_id] = {}

    def api(self, *args):
        if args[0].startswith(f'{self.repo}/git/ref/tags/'):
            return json.dumps({'object': {'type': 'commit', 'sha': self.tag_commit}})
        if args[:2] == ('--paginate', '--slurp'):
            path = args[2].removesuffix('?per_page=100')
            if path == f'{self.repo}/releases':
                return json.dumps([list(self.releases.values())])
            release_id = int(path.split('/')[-2])
            return json.dumps([list(self.assets[release_id].values())])
        self.assertEqual(args[0], '--method')
        if args[1] == 'DELETE':
            self.assertEqual(args[2], f'{self.repo}/releases/assets/42')
            for assets in self.assets.values():
                if assets.get('binary', {}).get('id') == 42:
                    del assets['binary']
            return ''
        release_id = int(args[2].rsplit('/', 1)[1])
        self.assertEqual(args[1:2] + args[3:-1], ('PATCH', '-F', 'draft=false', '-f'))
        release, = (entry for entry in self.releases.values() if entry['id'] == release_id)
        self.assertEqual(set(self.assets[release_id]), {'SHA256SUMS', 'binary'})
        release['draft'] = False
        if args[-1] == 'make_latest=true':
            self.latest = release['tag_name']
        else:
            self.assertEqual(args[-1], 'make_latest=false')
        return ''

    def release(self, args, **kwargs):
        if args[0] != 'gh':
            return self.run(args, **kwargs)
        _, _, command, tag, *options = args
        self.assertEqual(command, 'upload')
        assets = self.assets[self.releases[tag]['id']]
        for path in map(Path, options):
            self.assertNotIn(path.name, assets)
            if self.interrupt and path.name == 'binary':
                self.interrupt = False
                assets[path.name] = {'id': 42, 'name': path.name, 'state': 'starter'}
                raise subprocess.CalledProcessError(1, args)
            digest = 'sha256:' + hashlib.sha256(path.read_bytes()).hexdigest()
            assets[path.name] = {'name': path.name, 'state': 'uploaded', 'digest': digest}
            self.uploads.append((tag, path.name))

    def publish(self, tag):
        with (patch.object(publish, 'api', side_effect=self.api),
              patch.object(publish.subprocess, 'run', side_effect=self.release)):
            return publish.publish_downloads(self.repo, tag, self.commit, self.directory)

    def test_older_draft_resumes_without_moving_latest(self):
        self.draft('v0.9.0')
        self.draft('v0.9.1')
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish('v0.9.0')
        self.assertTrue(self.releases['v0.9.0']['draft'])
        self.assertTrue(self.publish('v0.9.1'))
        self.assertFalse(self.publish('v0.9.0'))
        self.assertFalse(self.releases['v0.9.0']['draft'])
        self.assertEqual(self.latest, 'v0.9.1')
        self.assertEqual([name for tag, name in self.uploads if tag == 'v0.9.0'], ['SHA256SUMS', 'binary'])
        self.assertFalse(self.publish('v0.9.0'))
        self.assertEqual(len(self.uploads), 4)

    def test_publishes_matching_draft_with_approved_notes(self):
        tag = 'v0.9.0'
        notes = '## Install fresh\nThere is no upgrade path from an earlier release.\n\nStatic ships off.\n'
        self.draft(tag, notes)
        self.interrupt = False

        self.assertTrue(self.publish(tag))
        self.assertFalse(self.releases[tag]['draft'])
        self.assertEqual(self.releases[tag]['body'], notes)
        self.assertEqual(self.latest, tag)

    def test_missing_or_empty_draft_stops_publication(self):
        tag = 'v0.9.0'
        for case in ('missing', 'different tag', 'empty'):
            with self.subTest(case=case):
                if case == 'different tag':
                    self.draft('v0.9.1')
                if case == 'empty':
                    self.draft(tag, ' \n')
                env = {'GITHUB_REPOSITORY': 'fixture/simple-host', 'APPROVED_DRAFT_TAG': ''}
                for command in (['check-stable-release'], ['images', str(self.directory), 'host', 'hack', 'standalone']):
                    argv = ['publish.py', command[0], tag, self.commit, *command[1:]]
                    with (patch.dict(publish.os.environ, env), patch.object(publish.sys, 'argv', argv),
                          patch.object(publish, 'api', side_effect=self.api),
                          patch.object(publish.subprocess, 'run') as skopeo,
                          self.assertRaisesRegex(SystemExit, f'approved release notes in a draft for {tag}')):
                        publish.main()
                    skopeo.assert_not_called()
                with self.assertRaisesRegex(SystemExit, f'approved release notes in a draft for {tag}'):
                    self.publish(tag)
                self.assertEqual(self.uploads, [])
                self.assertIsNone(self.latest)
                if case == 'empty':
                    self.assertTrue(self.releases[tag]['draft'])
                else:
                    self.assertNotIn(tag, self.releases)

    def test_conflicting_completed_asset_keeps_draft_unpublished(self):
        self.draft('v0.9.0')
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish('v0.9.0')
        self.assets[1]['SHA256SUMS']['digest'] = 'sha256:wrong'
        with self.assertRaisesRegex(SystemExit, 'SHA256SUMS: expected uploaded digest'):
            self.publish('v0.9.0')
        self.assertTrue(self.releases['v0.9.0']['draft'])
        self.assertIsNone(self.latest)

    def test_moved_tag_stops_download_publication(self):
        with patch.object(publish, 'api', return_value=json.dumps(
                {'object': {'type': 'commit', 'sha': 'b' * 40}})) as api:
            with self.assertRaisesRegex(SystemExit, 'does not point to'):
                publish.publish_downloads(self.repo, 'v0.9.0', self.commit, self.directory)
            api.assert_called_once_with(f'{self.repo}/git/ref/tags/v0.9.0')

    def test_tag_moved_during_upload_leaves_verified_assets_in_draft(self):
        self.interrupt = False
        tag = 'v0.9.0'
        self.draft(tag)

        def move_after_upload(args, **kwargs):
            result = self.release(args, **kwargs)
            if args[:3] == ['gh', 'release', 'upload']:
                self.tag_commit = 'b' * 40
            return result

        with (patch.object(publish, 'api', side_effect=self.api),
              patch.object(publish.subprocess, 'run', side_effect=move_after_upload),
              self.assertRaisesRegex(SystemExit, 'does not point to')):
            publish.publish_downloads(self.repo, tag, self.commit, self.directory)
        self.assertEqual(self.uploads, [(tag, 'SHA256SUMS'), (tag, 'binary')])
        self.assertTrue(self.releases[tag]['draft'])
        self.assertIsNone(self.latest)


class PromotionTest(unittest.TestCase):
    def test_only_latest_tag_at_built_commit_is_promoted(self):
        repo = 'repos/fixture/simple-host'
        tag = 'v0.9.0'
        commit = 'a' * 40
        source, destination = 'registry.test/host@sha256:digest', 'registry.test/host:latest'
        for latest, target in [('v0.9.1', commit), (tag, 'b' * 40), (tag, commit)]:
            replies = [latest, json.dumps({'object': {'type': 'tag', 'sha': 'c' * 40}}),
                       json.dumps({'object': {'type': 'commit', 'sha': target}})]
            with (self.subTest(latest=latest, target=target),
                  patch.object(publish, 'api', side_effect=replies),
                  patch.object(publish.subprocess, 'run') as copy):
                if target == commit:
                    publish.promote_latest(repo, tag, commit, source, destination)
                else:
                    with self.assertRaisesRegex(SystemExit, 'does not point to'):
                        publish.promote_latest(repo, tag, commit, source, destination)
                if latest == tag and target == commit:
                    copy.assert_called_once_with(['skopeo', 'copy', '--all', '--preserve-digests',
                                                  f'docker://{source}', f'docker://{destination}'], check=True)
                else:
                    copy.assert_not_called()


if __name__ == '__main__':
    unittest.main()
