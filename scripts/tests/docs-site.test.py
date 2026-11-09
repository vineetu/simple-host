#!/usr/bin/env python3
"""Offline regression checks for platform-independent Pagefind validation."""
import json
from pathlib import Path
import runpy
import tempfile
import unittest

docs = runpy.run_path(str(Path(__file__).resolve().parents[1] / 'docs-site.py'))


class PagefindCheckTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.tree = Path(tmp.name) / 'host'
        self.index = self.tree / 'pagefind'
        self.index.mkdir(parents=True)
        (self.tree / 'index.html').write_text('<main data-pagefind-body>Docs</main>')
        (self.tree / 'unindexed.html').write_text('<main>Not indexed</main>')
        self.entry = {'languages': {'en': {'hash': 'en_fixture', 'wasm': 'en', 'page_count': 1}}}
        self.write_entry()
        for name in ('pagefind.en_fixture.pf_meta', 'wasm.en.pagefind', 'wasm.unknown.pagefind'):
            (self.index / name).write_bytes(b'platform output')

    def write_entry(self):
        (self.index / 'pagefind-entry.json').write_text(json.dumps(self.entry))

    def check(self):
        return docs['check_pagefind'](self.tree)

    def test_valid_index_and_platform_independent_hash(self):
        self.assertEqual(self.check(), [])
        def digest():
            return docs['digest'](docs['output_files'](self.tree), self.tree)
        before = digest()
        (self.index / 'wasm.en.pagefind').write_bytes(b'another platform')
        self.assertEqual(digest(), before)
        (self.tree / 'index.html').write_text('<main data-pagefind-body>Changed</main>')
        self.assertNotEqual(digest(), before)

    def test_missing_directory(self):
        self.index.rename(self.tree / 'removed')
        self.assertIn('missing pagefind directory', self.check()[0])

    def test_invalid_or_missing_entry(self):
        entry = self.index / 'pagefind-entry.json'
        entry.write_text('{invalid')
        self.assertIn('invalid pagefind-entry.json', self.check()[0])
        entry.unlink()
        self.assertIn('invalid pagefind-entry.json', self.check()[0])

    def test_wrong_page_count(self):
        self.entry['languages']['en']['page_count'] = 2
        self.write_entry()
        self.assertIn('page_count', self.check()[0])

    def test_missing_references(self):
        for name in ('pagefind.en_fixture.pf_meta', 'wasm.en.pagefind', 'wasm.unknown.pagefind'):
            with self.subTest(name=name):
                path = self.index / name
                path.unlink()
                self.assertEqual(self.check(), [f'host: Pagefind reference missing: {name}'])
                path.write_bytes(b'platform output')

    def test_unknown_wasm_fallback(self):
        self.entry['languages']['en']['wasm'] = None
        self.write_entry()
        (self.index / 'wasm.en.pagefind').unlink()
        self.assertEqual(self.check(), [])


dr = docs['docs_reference']


class SlugifyTest(unittest.TestCase):
    def test_lowercases_and_hyphenates(self):
        self.assertEqual(dr.slugify('Custom Domains'), 'custom-domains')

    def test_strips_punctuation(self):
        self.assertEqual(dr.slugify('Storage resources!'), 'storage-resources')

    def test_empty_falls_back(self):
        self.assertEqual(dr.slugify('   '), 'x')


class EscapeTest(unittest.TestCase):
    def test_escapes_angle_brackets(self):
        self.assertEqual(dr.esc('a <handle> value'), 'a &lt;handle&gt; value')

    def test_none_is_empty(self):
        self.assertEqual(dr.esc(None), '')

    def test_table_cell_escapes_pipe_too(self):
        self.assertEqual(dr.cell('a|b <c>'), 'a\\|b &lt;c&gt;')


class HackFilterTest(unittest.TestCase):
    def test_drops_hack_parenthetical(self):
        text = 'Deletes the site (Simple Hack team sites excluded) right away.'
        self.assertEqual(dr.drop_hack_mentions(text, 'x'), 'Deletes the site right away.')

    def test_drops_whole_sentence_naming_hack(self):
        text = 'Lists this account sites. Hack team sites use a different route. Newest first.'
        self.assertEqual(dr.drop_hack_mentions(text, 'x'),
                          'Lists this account sites. Newest first.')

    def test_trims_conjunction_rather_than_whole_sentence(self):
        text = 'Signed notifications for writes on Simple Host and Simple Hack.'
        self.assertEqual(dr.drop_hack_mentions(text, 'x'),
                          'Signed notifications for writes on Simple Host.')

    def test_leaves_hackless_text_untouched(self):
        text = 'Nothing to see here.'
        self.assertEqual(dr.drop_hack_mentions(text, 'x'), text)

    def test_fails_loudly_when_everything_would_vanish(self):
        with self.assertRaises(ValueError):
            dr.drop_hack_mentions('Entirely about Simple Hack team sites.', 'where')

    def test_unknown_mcp_family_fails_loudly(self):
        with self.assertRaises(ValueError):
            dr.mcp_pages([{'name': 'x', 'family': 'not-a-real-family', 'listed': True, 'description': ''}], '/install.html')


if __name__ == '__main__':
    unittest.main()
