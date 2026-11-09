#!/usr/bin/env python3
"""Self-hosting/Enterprise pages and the changelog, rendered from repo files at
build time (not copied into docs/site), plus the .md-for-agents conversion of
every built doc page. Pure functions; called from docs-site.py's prepare() and
externalize()."""
import json
import re
from pathlib import Path

GITHUB_BLOB = 'https://github.com/vineetu/simple-host/blob/main/'
GITHUB_TREE = 'https://github.com/vineetu/simple-host/tree/main/'

# Rendered on both brands by filename, but these are about Simple Hack without
# "hack" in their own filename, so the Host skip list needs them named:
# hosted-events.md (running the server as the hackathon platform) and
# simple-hack.md (its operations runbook); kubernetes.md's only content is the
# Simple Hack Kubernetes chart (Host's Kubernetes path is Enterprise, linked
# separately from self-hosting/index.mdx, not this file).
HACK_EXTRA_SKIP = {'docs/advanced/hosted-events.md', 'docs/operations/simple-hack.md', 'docs/kubernetes.md'}

MONTHS = ['January', 'February', 'March', 'April', 'May', 'June', 'July',
          'August', 'September', 'October', 'November', 'December']


def repo_doc_files(root):
    """(repo_relpath, group, name) for every candidate repo doc file, in a
    stable order. 'name' is '' for a group's own overview/index page."""
    files = [('docs/configuration.md', 'configuration', '')]
    for p in sorted((root / 'docs/advanced').glob('*.md')):
        files.append((f'docs/advanced/{p.name}', 'settings', '' if p.name == 'README.md' else p.stem))
    for p in sorted((root / 'docs/platforms').glob('*.md')):
        files.append((f'docs/platforms/{p.name}', 'platforms', '' if p.name == 'README.md' else p.stem))
    for p in sorted((root / 'docs/operations').glob('*.md')):
        files.append((f'docs/operations/{p.name}', 'operations', p.stem))
    files.append(('docs/kubernetes.md', 'kubernetes', ''))
    return files


def is_hack_named(repo_path):
    """Skip this file entirely on the Host brand."""
    return 'hack' in Path(repo_path).name.lower() or repo_path in HACK_EXTRA_SKIP


def slug_for(group, name):
    if group == 'configuration':
        return 'self-hosting/configuration'
    if group == 'kubernetes':
        return 'self-hosting/kubernetes'
    return f'self-hosting/{group}/{name}' if name else f'self-hosting/{group}'


H1_RE = re.compile(r'^#[ \t]+(.+?)[ \t]*$', re.M)


def split_title(text):
    """The file's first H1 (title) and the body with that line dropped."""
    m = H1_RE.search(text)
    if not m:
        return '', text.lstrip('\n')
    body = text[:m.start()] + text[m.end():]
    return m[1].strip(), body.lstrip('\n')


# ---------------------------------------------------------------------------
# Host brand: drop every trace of Simple Hack from these repo docs. Crude by
# design (whole blocks at a time) rather than editing the source files, which
# a later ticket cleans up; the public requirement is zero case-insensitive
# "hack" surviving in the Host build, not a surgical edit.

FENCE_RE = re.compile(r'^(```+|~~~+)')
HEADING_RE = re.compile(r'^(#{2,6})[ \t]+(.*)$')
LIST_MARK_RE = re.compile(r'^(\s*)([-*+]|\d+[.)])\s')
COMMENT_LINE_RE = re.compile(r'^\s*<!--.*-->\s*$')


def _drop_hack_sections(text):
    """Drop a heading (any level) whose text mentions "hack", and everything
    under it until the next heading at the same or a shallower level. Lines
    inside a fenced code block are never read as headings (a shell comment or
    a Markdown example inside a fence can look like one): a fence closes only
    on a fence of the same character and at least as many repeats as opened
    it, matching CommonMark."""
    out = []
    drop_stack = []
    in_fence = False
    fence_marker = None
    for line in text.split('\n'):
        stripped = line.strip()
        if in_fence:
            if stripped.startswith(fence_marker):
                in_fence = False
            if not drop_stack:
                out.append(line)
            continue
        fm = FENCE_RE.match(stripped)
        if fm:
            in_fence = True
            fence_marker = fm.group(1)
            if not drop_stack:
                out.append(line)
            continue
        m = HEADING_RE.match(line)
        if m:
            level = len(m[1])
            while drop_stack and drop_stack[-1] >= level:
                drop_stack.pop()
            if 'hack' in m[2].lower():
                drop_stack.append(level)
                continue
            if drop_stack:
                continue
            out.append(line)
            continue
        if drop_stack:
            continue
        out.append(line)
    return '\n'.join(out)


def _split_blocks(text):
    """Blank-line-delimited blocks, with a fenced code block kept atomic."""
    blocks = []
    cur = []
    in_fence = False
    fence_marker = None
    for line in text.split('\n'):
        stripped = line.strip()
        if in_fence:
            cur.append(line)
            if stripped.startswith(fence_marker):
                in_fence = False
            continue
        fm = FENCE_RE.match(stripped)
        if fm:
            if cur:
                blocks.append(cur)
                cur = []
            in_fence = True
            fence_marker = fm.group(1)
            cur.append(line)
            continue
        if stripped == '':
            if cur:
                blocks.append(cur)
                cur = []
            blocks.append([line])
            continue
        cur.append(line)
    if cur:
        blocks.append(cur)
    return blocks


def _filter_list_items(lines):
    """Drop a list item that mentions "hack" together with every more deeply
    indented line that follows it (its own nested children), up to the next
    item at the same or a shallower indent. A sibling or ancestor that does
    not itself mention "hack" is evaluated, and kept, independently."""
    n = len(lines)
    indent = [len(LIST_MARK_RE.match(l)[1]) if LIST_MARK_RE.match(l) else None for l in lines]

    def item_mentions_hack(i):
        j = i + 1
        while j < n and indent[j] is None:
            j += 1
        return 'hack' in '\n'.join(lines[i:j]).lower()

    def item_end(i):
        j = i + 1
        while j < n and indent[j] is None:
            j += 1
        return j

    out = []
    skip = set()
    drop_stack = []  # indents of items currently being dropped
    for i, line in enumerate(lines):
        if i in skip:
            continue
        if indent[i] is not None:
            while drop_stack and drop_stack[-1] >= indent[i]:
                drop_stack.pop()
            if not drop_stack and item_mentions_hack(i):
                # Keep an item whose lead sentence is about Host: drop only
                # its Hack sentences.
                j = item_end(i)
                mark = LIST_MARK_RE.match(line)[0]
                body = ' '.join([line[len(mark):].strip()] + [l.strip() for l in lines[i + 1:j]])
                sentences = SENTENCE_RE.split(body)
                kept = [x for x in sentences if 'hack' not in x.lower()]
                nested = j < n and indent[j] > indent[i]
                if kept and kept[0] == sentences[0] and not nested:
                    out.append(mark + ' '.join(kept))
                    skip.update(range(i + 1, j))
                    continue
            if drop_stack or item_mentions_hack(i):
                drop_stack.append(indent[i])
                continue
            out.append(line)
            continue
        if drop_stack:
            continue
        out.append(line)
    return out


def _filter_table_rows(lines):
    """A Markdown table's own lines (header, separator, data...), ignoring
    any leading blank lines kept by the block splitter. Drop the whole table
    (return None) if its header row mentions "hack"; otherwise keep the
    header and separator and drop only the data rows that do."""
    content_idx = [i for i, l in enumerate(lines) if l.strip().startswith('|')]
    if not content_idx:
        return lines
    header_i = content_idx[0]
    if 'hack' in lines[header_i].lower():
        return None
    sep_i = content_idx[1] if len(content_idx) > 1 else None
    kept = []
    for i, l in enumerate(lines):
        if i in (header_i, sep_i) or l.strip() == '' or 'hack' not in l.lower():
            kept.append(l)
    return kept


def _filter_fence_block(lines):
    """A fenced code block's own lines (opening fence, body, closing fence).
    Drop only the body lines mentioning "hack", keeping the fence; drop the
    whole block (return None) if nothing is left inside it."""
    if 'hack' not in '\n'.join(lines).lower():
        return lines
    open_line = lines[0]
    fence_marker = FENCE_RE.match(open_line.strip())[1]
    has_close = len(lines) > 1 and lines[-1].strip().startswith(fence_marker)
    body = lines[1:-1] if has_close else lines[1:]
    kept_body = [l for l in body if 'hack' not in l.lower()]
    if not kept_body:
        return None
    result = [open_line] + kept_body
    if has_close:
        result.append(lines[-1])
    return result


def _drop_hack_blocks(text):
    out = []
    for block in _split_blocks(text):
        # A leading or trailing run of HTML-comment lines (e.g. the
        # `<!-- settings:group=... -->` / `<!-- /settings -->` pair that
        # wraps a settings table) belongs with the block it marks: classify
        # and filter what is between them, then keep the comments only if
        # that survives.
        prefix, suffix = [], []
        body = block
        while body and COMMENT_LINE_RE.match(body[0]):
            prefix.append(body[0])
            body = body[1:]
        while body and COMMENT_LINE_RE.match(body[-1]):
            suffix.insert(0, body[-1])
            body = body[:-1]
        if not body:
            out.extend(block)
            continue
        first = body[0].strip()
        joined = '\n'.join(body)
        if first == '':
            out.extend(block)
        elif FENCE_RE.match(first):
            filtered = _filter_fence_block(body)
            if filtered is not None:
                out.extend(prefix)
                out.extend(filtered)
                out.extend(suffix)
        elif all(l.strip() == '' or l.strip().startswith('|') for l in body):
            filtered = _filter_table_rows(body)
            if filtered is not None:
                out.extend(prefix)
                out.extend(filtered)
                out.extend(suffix)
        elif all(l.strip() == '' or l.strip().startswith('>') for l in body):
            out.extend(prefix)
            out.extend(l for l in body if 'hack' not in l.lower())
            out.extend(suffix)
        elif LIST_MARK_RE.match(first):
            out.extend(prefix)
            out.extend(_filter_list_items(body))
            out.extend(suffix)
        elif 'hack' not in joined.lower():
            out.extend(prefix)
            out.extend(body)
            out.extend(suffix)
        elif not first.startswith('#'):
            # Prose: drop only the sentences about Hack.
            kept = [x for x in SENTENCE_RE.split(' '.join(l.strip() for l in body)) if 'hack' not in x.lower()]
            if kept:
                out.extend(prefix)
                out.append(' '.join(kept))
                out.extend(suffix)
    return '\n'.join(out)


SENTENCE_RE = re.compile(r'(?<=[.!?])\s+(?=[A-Z`*\[(])')


def _drop_dangling_intros(text):
    """A line ending in ':' introduces what follows it (a list, a table, a
    code sample). When filtering has removed everything that followed -- end
    of section, or the next thing left is a heading -- the intro is now a
    dead end and is dropped too."""
    blocks = _split_blocks(text)
    n = len(blocks)
    out = []
    i = 0
    while i < n:
        block = blocks[i]
        if (block != [''] and not FENCE_RE.match(block[0].strip())
                and block[-1].rstrip().endswith(':')):
            j = i + 1
            while j < n and blocks[j] == ['']:
                j += 1
            if j >= n or HEADING_RE.match(blocks[j][0]):
                i += 1
                continue
        out.extend(block)
        i += 1
    return '\n'.join(out)


def _drop_empty_sections(text):
    """Drop a heading (any level) whose section -- everything before the next
    heading at the same or a shallower level -- is now blank lines only,
    ignoring fenced code blocks when scanning for that next heading."""
    lines = text.split('\n')
    n = len(lines)
    out = []
    i = 0
    while i < n:
        m = HEADING_RE.match(lines[i])
        if not m:
            out.append(lines[i])
            i += 1
            continue
        level = len(m[1])
        j = i + 1
        in_fence = False
        fence_marker = None
        while j < n:
            stripped = lines[j].strip()
            if in_fence:
                if stripped.startswith(fence_marker):
                    in_fence = False
                j += 1
                continue
            fm = FENCE_RE.match(stripped)
            if fm:
                in_fence = True
                fence_marker = fm.group(1)
                j += 1
                continue
            hm = HEADING_RE.match(lines[j])
            if hm and len(hm[1]) <= level:
                break
            j += 1
        if all(l.strip() == '' for l in lines[i + 1:j]):
            i = j
            continue
        out.append(lines[i])
        i += 1
    return '\n'.join(out)


PLACEHOLDER_DOMAIN = re.compile(r'\bhack\.example\.com\b', re.I)
# Reserved-name lists name the Hack product's own spellings; drop just those items.
HACK_LIST_ITEM = re.compile(r'\bsimple-?hack,\s*', re.I)


def host_filter(text):
    """Drop every section, paragraph, list item, table row, blockquote line
    and fenced code block mentioning "hack", for the Host brand only, then
    clean up what that filtering leaves behind: a heading whose section is
    now empty, and an intro line ending in ':' with nothing (or a heading)
    now after it. The docs' placeholder domain hack.example.com is only an
    example name, so it becomes example.com first instead of costing Host
    the rows and paragraphs that use it."""
    text = PLACEHOLDER_DOMAIN.sub('example.com', text)
    text = HACK_LIST_ITEM.sub('', text)
    text = _drop_hack_blocks(_drop_hack_sections(text))
    prev = None
    while text != prev:
        prev = text
        text = _drop_dangling_intros(text)
        text = _drop_empty_sections(text)
    return text


# ---------------------------------------------------------------------------
# Link and image rewriting.

LINK_RE = re.compile(r'(!?)\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)')
DIAGRAM_PREFIX = 'https://simple-host.app/diagrams/'


def rewrite_links(text, root, repo_path, url_for, copy_image, is_skipped=lambda rel: False):
    """url_for(repo_relpath) -> page URL or None (not rendered on this brand).
    copy_image(repo_relpath) -> published URL, copying the file as a side
    effect. is_skipped(repo_relpath) -> True when the target is a real repo
    file deliberately left out of this brand (a Hack-only file on Host): the
    link is dropped, keeping just its text, rather than falling back to a
    GitHub link that would point a Host reader at Hack-only content."""
    base_dir = (root / repo_path).parent

    def repl(m):
        bang, label, target = m[1], m[2], m[3]
        if target.startswith('http://') or target.startswith('https://'):
            if bang and target.startswith(DIAGRAM_PREFIX):
                return f'{bang}[{label}](/diagrams/{target[len(DIAGRAM_PREFIX):]})'
            return m[0]
        if target.startswith('/') or target.startswith('#'):
            return m[0]
        path, frag = (target.split('#', 1) + [''])[:2] if '#' in target else (target, '')
        is_dir = path.endswith('/')
        try:
            resolved = (base_dir / path).resolve()
            rel = resolved.relative_to(root).as_posix()
        except ValueError:
            return m[0]
        if bang:
            return f'{bang}[{label}]({copy_image(rel)})'
        page_url = url_for(rel)
        if page_url:
            return f'[{label}]({page_url}{"#" + frag if frag else ""})'
        if is_skipped(rel):
            return label
        if is_dir:
            return f'[{label}]({GITHUB_TREE}{rel}/)'
        return f'[{label}]({GITHUB_BLOB}{rel})'

    return LINK_RE.sub(repl, text)


def copy_asset(root, source_dir):
    """Returns a copy_image callback that stages a repo image this brand's
    pages reference (in .generated-content/_assets, which the content loader
    ignores); write_md_copies() ships it next to the built pages and the
    callback returns its /docs/ URL."""
    dest_dir = source_dir / '.generated-content/_assets'

    def copy_image(rel):
        name = Path(rel).name
        data = (root / rel).read_bytes()
        dest_dir.mkdir(parents=True, exist_ok=True)
        (dest_dir / name).write_bytes(data)
        return f'/docs/self-hosting/assets/{name}'

    return copy_image


# ---------------------------------------------------------------------------
# Changelog.

CHANGELOG_RE = re.compile(r'^(\d{4})-(\d{2})-(\d{2}):\s*(.*)$')
# Only a parenthetical made up ENTIRELY of ticket references, e.g. "(SIM-391)"
# or "(SIM-388/SIM-393)", is removable noise. A bare SIM-NNN that is not
# parenthesized (a direct object: "a run of SIM-345"), or a parenthetical that
# mixes a ticket with other words (e.g. "(SIM-211 follow-up)" or "(agent
# notifications phase 2, SIM-356)"), is left exactly as written -- stripping
# those used to mangle the sentence around the removed token.
TICKET_PAREN_RE = re.compile(r'\s*\(SIM-\d+(?:\s*[/,]\s*SIM-\d+)*\)')


def parse_changelog(text):
    """[(year, month, day, body), ...] in file order (newest first)."""
    entries = []
    for line in text.split('\n'):
        m = CHANGELOG_RE.match(line)
        if m:
            entries.append(m.groups())
    return entries


def strip_tickets(text):
    text = TICKET_PAREN_RE.sub('', text)
    text = re.sub(r'[ \t]{2,}', ' ', text)
    return text.strip()


def render_changelog(entries, drop_hack):
    out = []
    cur_key = None
    for year, month, day, body in entries:
        if drop_hack and 'hack' in body.lower():
            continue
        key = (year, month)
        if key != cur_key:
            out.append(f'## {MONTHS[int(month) - 1]} {year}')
            cur_key = key
        out.append(f'- **{MONTHS[int(month) - 1][:3]} {int(day)}:** {strip_tickets(body)}')
    return '\n\n'.join(out) + '\n' if out else ''


# ---------------------------------------------------------------------------
# prepare(): write the generated self-hosting pages, the changelog page and
# the manifest astro.config.mjs and self-hosting/index.mdx read.

CODE_SPAN_RE = re.compile(r'(`+)(.+?)\1')
RAW_ANGLE_RE = re.compile(r'<(?!!--|https?://)')


def escape_angles(text):
    """The repo docs write placeholders such as <handle> in prose; Markdown
    would read them as HTML tags. Escape every '<' outside code (fences and
    backtick spans), except HTML comments and <https://...> autolinks."""
    out, fence = [], None
    for line in text.split('\n'):
        m = FENCE_RE.match(line.strip())
        if fence:
            if m and m.group(0)[0] == fence[0] and len(m.group(0)) >= len(fence):
                fence = None
            out.append(line)
            continue
        if m:
            fence = m.group(0)
            out.append(line)
            continue
        parts, last = [], 0
        for c in CODE_SPAN_RE.finditer(line):
            parts.append(RAW_ANGLE_RE.sub('&lt;', line[last:c.start()]))
            parts.append(c.group(0))
            last = c.end()
        parts.append(RAW_ANGLE_RE.sub('&lt;', line[last:]))
        out.append(''.join(parts))
    return '\n'.join(out)


def _frontmatter(title, body):
    safe_title = title.replace('"', '\\"')
    return f'---\ntitle: "{safe_title}"\n---\n\n{body}\n'


def generate(root, target, brand):
    """target is docs/site/.generated-content for this brand's prepare()."""
    files = repo_doc_files(root)
    host = brand == 'host'
    rendered = [(p, g, n) for p, g, n in files if not (host and is_hack_named(p))]
    url_for_map = {p: f'/docs/{slug_for(g, n)}/' for p, g, n in rendered}
    url_for = url_for_map.get
    copy_image = copy_asset(root, root / 'docs/site')

    manifest = {}
    for repo_path, group, name in rendered:
        text = (root / repo_path).read_text()
        title, body = split_title(text)
        if host:
            body = host_filter(body)
        body = rewrite_links(body, root, repo_path, url_for, copy_image,
                              lambda rel: host and is_hack_named(rel))
        slug = slug_for(group, name)
        dest = target / slug / 'index.md'
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(_frontmatter(title, escape_angles(body)))
        entry = {'label': title, 'slug': slug}
        if group in ('configuration', 'kubernetes'):
            manifest[group] = entry
        elif name == '':
            manifest.setdefault(group, {'label': group.capitalize(), 'items': []})['overview'] = entry
        else:
            manifest.setdefault(group, {'label': group.capitalize(), 'items': []})['items'].append(entry)
    manifest.setdefault('settings', {'label': 'Settings', 'items': []})['label'] = 'Settings'
    manifest.setdefault('platforms', {'label': 'Platforms', 'items': []})['label'] = 'Platforms'
    manifest.setdefault('operations', {'label': 'Operations', 'items': []})['label'] = 'Operations'

    pages_list = [manifest['configuration']]
    pages_list += [manifest['settings'].get('overview')] if manifest['settings'].get('overview') else []
    pages_list += manifest['settings']['items']
    pages_list += [manifest['platforms'].get('overview')] if manifest['platforms'].get('overview') else []
    pages_list += manifest['platforms']['items']
    pages_list += manifest['operations']['items']
    if 'kubernetes' in manifest:
        pages_list.append(manifest['kubernetes'])
    list_md = '\n'.join(f"- [{p['label']}](/docs/{p['slug']}/)" for p in pages_list)
    home = target / 'self-hosting/index.mdx'
    if home.is_file():
        home.write_text(home.read_text().rstrip('\n') + '\n\n' + list_md + '\n')

    changelog_text = (root / 'CHANGELOG.md').read_text()
    rendered_changelog = render_changelog(parse_changelog(changelog_text), host)
    (target / 'changelog.md').write_text(_frontmatter('Changelog', escape_angles(rendered_changelog)))
    manifest['changelog'] = {'label': 'Changelog', 'slug': 'changelog'}

    (target / 'self-hosting-pages.json').write_text(json.dumps(manifest, indent=2) + '\n')


# ---------------------------------------------------------------------------
# externalize(): write the plain-Markdown ".md" copy of every built page,
# converting MDX components into Markdown and the runtime-filter markers the
# Go handler already understands for the HTML.

FRONTMATTER_RE = re.compile(r'\A---\n(.*?)\n---\n', re.S)
TITLE_RE = re.compile(r'^title:\s*"?(.*?)"?\s*$', re.M)
SLUG_RE = re.compile(r'^slug:\s*"?(.*?)"?\s*$', re.M)
# A Markdown link into another built page (never an image: those keep
# pointing at the copied asset). Only a directory-style target -- "/docs/" or
# "/docs/<path>/", with an optional #fragment -- is a page link; it is
# rewritten to that page's own .md sibling so an agent walking the docs from
# /docs/index.md stays in Markdown throughout, per llms.txt. A target with no
# trailing slash (an asset, a script) is left alone.
MD_PAGE_LINK_RE = re.compile(r'(?<!!)\[([^\]]*)\]\(/docs/([^)\s#]*)(#[^)\s]*)?\)')


def _rewrite_md_page_link(m):
    label, path, frag = m[1], m[2], m[3] or ''
    if path != '' and not path.endswith('/'):
        return m[0]
    new_path = '/docs/index.md' if path == '' else f'/docs/{path[:-1]}.md'
    return f'[{label}]({new_path}{frag})'
IMPORT_RE = re.compile(r'^import .+?;?[ \t]*$\n?', re.M)
COMMENT_RE = re.compile(r'\{/\*.*?\*/\}', re.S)
WHAT_RE = re.compile(r'<What\s+id="([^"]+)"\s*/>')
STEPS_RE = re.compile(r'</?Steps>\n?')
CARDGRID_RE = re.compile(r'</?CardGrid>\n?')
LINKCARD_RE = re.compile(r'<LinkCard\b((?:\s+\w+="[^"]*")+)\s*/>')
LINKBUTTON_RE = re.compile(r'<LinkButton\s+href="([^"]*)">(.*?)</LinkButton>', re.S)
ATTR_RE = re.compile(r'(\w+)="([^"]*)"')
TABS_OPEN_RE = re.compile(r'<Tabs[^>]*>\n?')
TABS_CLOSE_RE = re.compile(r'</Tabs>\n?')
TABITEM_OPEN_RE = re.compile(r'<TabItem\s+label="([^"]*)"[^>]*>')
TABITEM_CLOSE_RE = re.compile(r'</TabItem>\n?')
INSTANCE_RE = re.compile(r'<Instance\s+kind="(hosted|self)"\s*>|</Instance>')


def _linkcard_repl(m):
    attrs = dict(ATTR_RE.findall(m[1]))
    return f"- [{attrs.get('title', '')}]({attrs.get('href', '')})"


def convert_to_md(text):
    """One built page's brand-selected source (.md or .mdx) to the plain
    Markdown served at its .md URL for agents."""
    title = ''
    m = FRONTMATTER_RE.match(text)
    if m:
        tm = TITLE_RE.search(m[1])
        if tm:
            # _frontmatter() backslash-escapes a '"' in the title for the YAML
            # value; undo that for the plain-text .md title, or a literal
            # backslash-quote would survive into the served page.
            title = tm[1].replace('\\"', '"')
        text = text[m.end():]
    text = IMPORT_RE.sub('', text)
    # A removed {/* ... */} comment leaves its surrounding blank lines behind
    # (e.g. self-hosting/index.mdx's build-note comment between the intro
    # line and the appended page list); collapse the run rather than carry
    # three blank lines into the served .md.
    text = re.sub(r'\n[ \t]*\n(?:[ \t]*\n)+', '\n\n', COMMENT_RE.sub('', text))
    text = WHAT_RE.sub(lambda mm: f'[What this is](/features.html#{mm[1]})', text)
    text = STEPS_RE.sub('', text)
    text = CARDGRID_RE.sub('', text)
    text = LINKCARD_RE.sub(_linkcard_repl, text)
    text = LINKBUTTON_RE.sub(lambda mm: f'[{mm[2]}]({mm[1]})', text)
    text = TABS_OPEN_RE.sub('', text)
    text = TABS_CLOSE_RE.sub('', text)
    text = TABITEM_OPEN_RE.sub(lambda mm: f'**{mm[1]}**\n', text)
    text = TABITEM_CLOSE_RE.sub('', text)
    stack = []

    def inst(mm):
        if mm[1]:
            stack.append(mm[1])
            return f'<!--sh:{mm[1]}-->'
        kind = stack.pop() if stack else 'hosted'
        return f'<!--/sh:{kind}-->'

    text = INSTANCE_RE.sub(inst, text)
    text = MD_PAGE_LINK_RE.sub(_rewrite_md_page_link, text)
    body = text.strip('\n')
    header = f'# {title}\n\n' if title else ''
    return header + body + '\n'


def slug_for_source(rel_posix, text=''):
    """A .generated-content-relative POSIX path (no extension) to its
    Starlight slug, matching Astro's glob-loader index convention -- unless
    the page's own frontmatter declares `slug:`, which overrides the
    path-derived one (e.g. start/run.mdx with `slug: start` is the brand's
    index page for start/, built to start/index.html, not start/run/)."""
    fm = FRONTMATTER_RE.match(text)
    if fm:
        sm = SLUG_RE.search(fm[1])
        if sm:
            return sm[1]
    s = rel_posix.rsplit('.', 1)[0]
    if s == 'index':
        return ''
    if s.endswith('/index'):
        return s[:-len('/index')]
    return s


def write_md_copies(root, brand):
    source_dir = root / 'docs/site/.generated-content'
    out_dir = root / 'internal/webassets/docs-site' / brand
    assets = source_dir / '_assets'
    if assets.is_dir():
        for p in sorted(assets.iterdir()):
            dest = out_dir / 'self-hosting/assets' / p.name
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(p.read_bytes())
    for p in sorted(source_dir.rglob('*')):
        if not p.is_file() or p.suffix not in ('.md', '.mdx'):
            continue
        rel = p.relative_to(source_dir).as_posix()
        text = p.read_text()
        slug = slug_for_source(rel, text)
        md = convert_to_md(text)
        dest = out_dir / (f'{slug}.md' if slug else 'index.md')
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(md)
