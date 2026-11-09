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


def slugify(text):
    """Approximate the heading-id slugger Starlight/rehype-slug uses, for
    rewriting markdown link fragments."""
    text = text.strip().lower()
    text = re.sub(r'[`*_]', '', text)
    text = re.sub(r'[^a-z0-9\- ]+', '', text)
    text = re.sub(r'\s+', '-', text)
    text = re.sub(r'-+', '-', text).strip('-')
    return text


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

FENCE_RE = re.compile(r'^(```|~~~)')
HEADING_RE = re.compile(r'^(#{2,6})[ \t]+(.*)$')
LIST_MARK_RE = re.compile(r'^(\s*)([-*+]|\d+[.)])\s')


def _drop_hack_sections(text):
    """Drop a heading (any level) whose text mentions "hack", and everything
    under it until the next heading at the same or a shallower level."""
    out = []
    drop_stack = []
    for line in text.split('\n'):
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
    items = []
    cur = None
    for line in lines:
        if LIST_MARK_RE.match(line):
            if cur is not None:
                items.append(cur)
            cur = [line]
        else:
            cur = [line] if cur is None else cur + [line]
    if cur is not None:
        items.append(cur)
    kept = []
    for item in items:
        if 'hack' not in '\n'.join(item).lower():
            kept.extend(item)
    return kept


def _drop_hack_blocks(text):
    out = []
    for block in _split_blocks(text):
        first = block[0].strip()
        joined = '\n'.join(block)
        if first == '':
            out.extend(block)
        elif FENCE_RE.match(first):
            if 'hack' not in joined.lower():
                out.extend(block)
        elif all(l.strip() == '' or l.strip().startswith('|') for l in block):
            out.extend(l for l in block if 'hack' not in l.lower())
        elif all(l.strip() == '' or l.strip().startswith('>') for l in block):
            out.extend(l for l in block if 'hack' not in l.lower())
        elif LIST_MARK_RE.match(first):
            out.extend(_filter_list_items(block))
        elif 'hack' not in joined.lower():
            out.extend(block)
    return '\n'.join(out)


def host_filter(text):
    """Drop every section, paragraph, list item, table row, blockquote line
    and fenced code block mentioning "hack", for the Host brand only."""
    return _drop_hack_blocks(_drop_hack_sections(text))


# ---------------------------------------------------------------------------
# Link and image rewriting.

LINK_RE = re.compile(r'(!?)\[([^\]]*)\]\(([^)\s]+)(?:\s+"[^"]*")?\)')
DIAGRAM_PREFIX = 'https://simple-host.app/diagrams/'


def rewrite_links(text, root, repo_path, url_for, copy_image):
    """url_for(repo_relpath) -> page URL or None (not rendered on this brand).
    copy_image(repo_relpath) -> published URL, copying the file as a side
    effect."""
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
TICKET_PAREN_RE = re.compile(r'\s*\(SIM-\d+(?:\s*[/,]\s*SIM-\d+)*\)')
# Eats a run of leading whitespace along with the bare ticket, so removal
# never leaves a stray space before whatever punctuation follows (and never
# touches unrelated punctuation elsewhere in the line).
TICKET_BARE_RE = re.compile(r'[ \t]*(?<![\w-])SIM-\d+(?![\w-])')


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
    text = TICKET_BARE_RE.sub('', text)
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
        body = rewrite_links(body, root, repo_path, url_for, copy_image)
        slug = slug_for(group, name)
        dest = target / slug / 'index.md'
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(_frontmatter(title, body))
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
    (target / 'changelog.md').write_text(_frontmatter('Changelog', rendered_changelog))
    manifest['changelog'] = {'label': 'Changelog', 'slug': 'changelog'}

    (target / 'self-hosting-pages.json').write_text(json.dumps(manifest, indent=2) + '\n')


# ---------------------------------------------------------------------------
# externalize(): write the plain-Markdown ".md" copy of every built page,
# converting MDX components into Markdown and the runtime-filter markers the
# Go handler already understands for the HTML.

FRONTMATTER_RE = re.compile(r'\A---\n(.*?)\n---\n', re.S)
TITLE_RE = re.compile(r'^title:\s*"?(.*?)"?\s*$', re.M)
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
            title = tm[1]
        text = text[m.end():]
    text = IMPORT_RE.sub('', text)
    text = COMMENT_RE.sub('', text)
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
    body = text.strip('\n')
    header = f'# {title}\n\n' if title else ''
    return header + body + '\n'


def slug_for_source(rel_posix):
    """A .generated-content-relative POSIX path (no extension) to its
    Starlight slug, matching Astro's glob-loader index convention."""
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
        slug = slug_for_source(rel)
        md = convert_to_md(p.read_text())
        dest = out_dir / (f'{slug}.md' if slug else 'index.md')
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(md)
