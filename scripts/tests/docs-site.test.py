#!/usr/bin/env python3
"""Offline regression checks for platform-independent Pagefind validation."""
import json
from pathlib import Path
import runpy
import tempfile
import unittest

docs = runpy.run_path(str(Path(__file__).resolve().parents[1] / 'docs-site.py'))
pages = runpy.run_path(str(Path(__file__).resolve().parents[1] / 'docs-repo-pages.py'))


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
class RepoPagesLinkRewriteTest(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        (self.root / 'docs/advanced').mkdir(parents=True)
        (self.root / 'docs/platforms').mkdir(parents=True)
        (self.root / 'docs/other.md').write_text('unrendered')
        self.url_for = {'docs/advanced/email.md': '/docs/self-hosting/settings/email/'}.get
        self.copied = []

        def copy_image(rel):
            self.copied.append(rel)
            return f'/docs/self-hosting/assets/{Path(rel).name}'

        self.copy_image = copy_image

    def rewrite(self, text, repo_path='docs/advanced/accounts-and-sign-in.md'):
        return pages['rewrite_links'](text, self.root, repo_path, self.url_for, self.copy_image)

    def test_rendered_sibling_becomes_a_docs_page(self):
        out = self.rewrite('See [Email](email.md) for sending mail.')
        self.assertEqual(out, 'See [Email](/docs/self-hosting/settings/email/) for sending mail.')

    def test_fragment_is_preserved(self):
        out = self.rewrite('See [Email](email.md#reply-to).')
        self.assertEqual(out, 'See [Email](/docs/self-hosting/settings/email/#reply-to).')

    def test_unrendered_sibling_becomes_a_github_blob_link(self):
        out = self.rewrite('See [other](../other.md).')
        self.assertEqual(out, 'See [other](https://github.com/vineetu/simple-host/blob/main/docs/other.md).')

    def test_non_repo_file_becomes_a_github_blob_link(self):
        (self.root / 'docs/advanced/settings.json').write_text('{}')
        out = self.rewrite('See [settings](settings.json).')
        self.assertEqual(out, 'See [settings](https://github.com/vineetu/simple-host/blob/main/docs/advanced/settings.json).')

    def test_directory_link_becomes_a_github_tree_link(self):
        out = self.rewrite('See [deploy](../../deploy/platforms/coolify/).')
        self.assertEqual(out, 'See [deploy](https://github.com/vineetu/simple-host/tree/main/deploy/platforms/coolify/).')

    def test_absolute_diagram_image_is_rewritten(self):
        out = self.rewrite('![a diagram](https://simple-host.app/diagrams/coolify.svg)')
        self.assertEqual(out, '![a diagram](/diagrams/coolify.svg)')

    def test_repo_image_is_copied_and_linked(self):
        out = self.rewrite('![shot](assets/shot.png)', repo_path='docs/platforms/coolify.md')
        self.assertEqual(out, '![shot](/docs/self-hosting/assets/shot.png)')
        self.assertEqual(self.copied, ['docs/platforms/assets/shot.png'])

    def test_absolute_and_external_links_pass_through(self):
        out = self.rewrite('See [us](/docs/guides/publish/) or [them](https://example.com/x).')
        self.assertEqual(out, 'See [us](/docs/guides/publish/) or [them](https://example.com/x).')


class RepoPagesHostFilterTest(unittest.TestCase):
    def test_section_with_hack_heading_is_dropped(self):
        text = '## Keep\nkeep this\n\n## About Simple Hack\ndrop this\n\n## Also keep\nkept too\n'
        out = pages['host_filter'](text)
        self.assertNotIn('drop this', out)
        self.assertIn('keep this', out)
        self.assertIn('kept too', out)

    def test_nested_heading_under_dropped_section_also_drops(self):
        text = '## About Simple Hack\n### Detail\ndrop\n\n## Keep\nkeep\n'
        out = pages['host_filter'](text)
        self.assertNotIn('drop', out)
        self.assertIn('keep', out)

    def test_paragraph_mentioning_hack_is_dropped_whole(self):
        text = 'Host-only line.\n\nThis line mentions Simple Hack somewhere\nacross two lines.\n\nAnother host line.\n'
        out = pages['host_filter'](text)
        self.assertNotIn('Hack', out)
        self.assertIn('Host-only line.', out)
        self.assertIn('Another host line.', out)

    def test_list_item_mentioning_hack_is_dropped_not_the_whole_list(self):
        text = '- keep one\n- mentions hack here\n- keep two\n'
        out = pages['host_filter'](text)
        self.assertNotIn('hack', out)
        self.assertIn('keep one', out)
        self.assertIn('keep two', out)

    def test_table_row_mentioning_hack_is_dropped(self):
        text = '| A | B |\n|---|---|\n| x | y |\n| simple-hack row | z |\n'
        out = pages['host_filter'](text)
        self.assertNotIn('hack', out)
        self.assertIn('| x | y |', out)

    def test_fenced_code_block_mentioning_hack_is_dropped(self):
        text = 'keep\n\n```\nSIMPLE_HACK_IMAGE=x\n```\n\nalso keep\n'
        out = pages['host_filter'](text)
        self.assertNotIn('hack', out.lower())
        self.assertIn('keep', out)
        self.assertIn('also keep', out)


class RepoPagesChangelogTest(unittest.TestCase):
    def test_grouped_by_month_newest_first_with_tickets_stripped(self):
        text = (
            '2026-11-08: Did a thing (SIM-391).\n'
            '2026-11-07: Did another SIM-200 thing.\n'
            '2026-10-30: Older thing.\n'
        )
        entries = pages['parse_changelog'](text)
        out = pages['render_changelog'](entries, drop_hack=False)
        self.assertEqual(out, (
            '## November 2026\n\n'
            '- **Nov 8:** Did a thing.\n\n'
            '- **Nov 7:** Did another thing.\n\n'
            '## October 2026\n\n'
            '- **Oct 30:** Older thing.\n'
        ))

    def test_host_drops_hack_entries(self):
        entries = pages['parse_changelog']('2026-11-08: Simple Hack got a feature.\n2026-11-08: Simple Host got a feature.\n')
        out = pages['render_changelog'](entries, drop_hack=True)
        self.assertNotIn('Hack', out)
        self.assertIn('Simple Host got a feature.', out)


class RepoPagesMarkdownConversionTest(unittest.TestCase):
    def convert(self, text):
        return pages['convert_to_md'](text)

    def test_frontmatter_becomes_h1_and_imports_drop(self):
        text = '---\ntitle: "Agents"\n---\n\nimport { Steps } from \'@astrojs/starlight/components\';\n\nBody text.\n'
        out = self.convert(text)
        self.assertTrue(out.startswith('# Agents\n\n'))
        self.assertNotIn('import ', out)
        self.assertIn('Body text.', out)

    def test_what_component_becomes_a_link(self):
        out = self.convert('---\ntitle: "T"\n---\n<What id="admin-console" />\n')
        self.assertIn('[What this is](/features.html#admin-console)', out)

    def test_steps_wrapper_is_dropped(self):
        out = self.convert('---\ntitle: "T"\n---\n<Steps>\n\n1. One\n2. Two\n\n</Steps>\n')
        self.assertNotIn('<Steps>', out)
        self.assertNotIn('</Steps>', out)
        self.assertIn('1. One', out)

    def test_tabs_become_bold_labels(self):
        out = self.convert('---\ntitle: "T"\n---\n<Tabs syncKey="way">\n<TabItem label="macOS">mac steps</TabItem>\n<TabItem label="Linux">linux steps</TabItem>\n</Tabs>\n')
        self.assertIn('**macOS**', out)
        self.assertIn('**Linux**', out)
        self.assertNotIn('<Tabs', out)
        self.assertNotIn('<TabItem', out)

    def test_instance_becomes_sh_markers(self):
        out = self.convert('---\ntitle: "T"\n---\n<Instance kind="hosted">hosted text</Instance><Instance kind="self">self text</Instance>\n')
        self.assertIn('<!--sh:hosted-->hosted text<!--/sh:hosted-->', out)
        self.assertIn('<!--sh:self-->self text<!--/sh:self-->', out)

    def test_linkcard_and_linkbutton_become_links(self):
        out = self.convert('---\ntitle: "T"\n---\n<CardGrid>\n<LinkCard title="Storage" href="/docs/guides/storage/" />\n</CardGrid>\n<LinkButton href="/install.html">Connect</LinkButton>\n')
        self.assertIn('- [Storage](/docs/guides/storage/)', out)
        self.assertIn('[Connect](/install.html)', out)
        self.assertNotIn('<CardGrid>', out)
        self.assertNotIn('<LinkCard', out)


if __name__ == '__main__':
    unittest.main()
