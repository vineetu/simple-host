#!/usr/bin/env python3
"""Checks that the committed docs build is current and internally sound:
(a) output matches its recorded source/reference digests (docs-site.py check);
(b) every public operation/tool is documented exactly once per brand;
(c) every internal docs/features/llms/skills link resolves, page and fragment;
(e) limitstext.go's exact phrases survive into the built HTML verbatim;
(f) the Host build never says "hack".

One line per problem on failure, non-zero exit. Run from scripts/check-docs-sync.sh.
"""
import importlib.util
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'docs/site'
OUTPUT = ROOT / 'internal/webassets/docs-site'
BRANDS = ('host', 'hack')

_spec = importlib.util.spec_from_file_location('docs_site', ROOT / 'scripts/docs-site.py')
docs_site = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(docs_site)
dr = docs_site.docs_reference

SKILLS_DIR = {'host': ROOT / 'internal/webassets/simple-host-website/skills',
              'hack': ROOT / 'internal/webassets/hack-toolkit/skills'}
BRAND_DOMAIN = {'host': 'simple-host.app', 'hack': 'simple-hack.app'}


def problems_current():
    try:
        docs_site.check()
    except SystemExit as exc:
        return str(exc).splitlines()
    return []


def expected_markers(brand):
    tools = dr.run_inventory('tools-json', brand)
    spec = dr.run_inventory('openapi-json', brand)
    ops = {f'{method.upper()} {path}' for tag, path, method, op, sp in dr.operation_entries(spec)}
    names = {t['name'] for t in tools}
    return ops, names


def problems_markers():
    errors = []
    for brand in BRANDS:
        tree = OUTPUT / brand
        if not tree.is_dir():
            errors.append(f'{brand}: no built tree, run scripts/build-docs-site.sh')
            continue
        try:
            expected_ops, expected_tools = expected_markers(brand)
        except Exception as exc:  # noqa: BLE001 - surface any Go/JSON failure as a problem line
            errors.append(f'{brand}: could not compute expected markers: {exc}')
            continue
        op_counts, tool_counts = {}, {}
        for page in tree.rglob('*.html'):
            body = page.read_text()
            for m in re.finditer(r'data-operation="([^"]*)"', body):
                op_counts[m[1]] = op_counts.get(m[1], 0) + 1
            for m in re.finditer(r'data-tool="([^"]*)"', body):
                tool_counts[m[1]] = tool_counts.get(m[1], 0) + 1
        for op in sorted(expected_ops):
            if op_counts.get(op, 0) != 1:
                errors.append(f'{brand}: operation documented {op_counts.get(op, 0)} times (want 1): {op}')
        for op in sorted(set(op_counts) - expected_ops):
            errors.append(f'{brand}: data-operation marker for an operation not in the public spec: {op}')
        for name in sorted(expected_tools):
            if tool_counts.get(name, 0) != 1:
                errors.append(f'{brand}: tool documented {tool_counts.get(name, 0)} times (want 1): {name}')
        for name in sorted(set(tool_counts) - expected_tools):
            errors.append(f'{brand}: data-tool marker for a tool not in that edition: {name}')
    return errors


HREF_RE = re.compile(r'''href=["']([^"']+)["']''')
ID_RE = re.compile(r'''\bid=["']([^"']+)["']''')


def page_ids(path):
    return set(ID_RE.findall(path.read_text()))


def resolve_docs_path(tree, url_path):
    """/docs/foo/bar/ (or without the /docs prefix, already stripped) -> the
    built file it names, or None. Trailing slash => .../index.html."""
    rel = url_path.strip('/')
    candidates = [tree / rel / 'index.html', tree / rel]
    if rel == '':
        candidates = [tree / 'index.html']
    for c in candidates:
        if c.is_file():
            return c
    return None


def split_href(href):
    href = href.split('#', 1)
    path = href[0]
    frag = href[1] if len(href) > 1 else None
    return path, frag


def check_docs_link(brand, path, frag, cache):
    """path is the /docs/... part (no leading /docs), already brand-relative."""
    tree = OUTPUT / brand
    key = (brand, path)
    if key not in cache:
        cache[key] = resolve_docs_path(tree, path)
    target = cache[key]
    if target is None:
        return f'{brand}: broken link /docs/{path.strip("/")}'
    if frag:
        if ('ids', key) not in cache:
            cache[('ids', key)] = page_ids(target)
        if frag not in cache[('ids', key)]:
            return f'{brand}: broken fragment /docs/{path.strip("/")}#{frag}'
    return None


def docs_relative(href, brand):
    """Return the /docs/-relative path+fragment for href if it is a link into
    this brand's docs tree (relative /docs/... or absolute https://<domain>/docs/...),
    else None."""
    for prefix in (f'https://{BRAND_DOMAIN[brand]}/docs/', f'http://{BRAND_DOMAIN[brand]}/docs/'):
        if href.startswith(prefix):
            return href[len(prefix) - 1:]  # keep the leading '/'... actually strip to path
    if href.startswith('/docs/') or href == '/docs':
        return href[len('/docs'):] or '/'
    return None


def problems_links():
    errors = []
    cache = {}
    for brand in BRANDS:
        tree = OUTPUT / brand
        if not tree.is_dir():
            continue
        for page in sorted(tree.rglob('*.html')):
            body = page.read_text()
            for href in HREF_RE.findall(body):
                rel = docs_relative(href, brand)
                if rel is None:
                    continue
                docs_path, frag = split_href(rel)
                err = check_docs_link(brand, docs_path, frag, cache)
                if err:
                    errors.append(f'{err} (linked from {page.relative_to(ROOT)})')

    # features.json "Docs" links (SIM-390 item 4d): every entry must resolve
    # per brand, AND every feature not in not_shown with a brand on (yes/partial)
    # must carry at least one /docs/ link that resolves in every such brand.
    features_path = ROOT / 'internal/webassets/static/features.json'
    features = json.loads(features_path.read_text())
    for area in features.get('areas', []):
        for feature in area.get('features', []):
            docs_links = feature.get('docs', []) or []
            needed = [b for b in BRANDS if feature.get('editions', {}).get(b) in ('yes', 'partial')]
            has_resolving_docs_link = False
            for link in docs_links:
                href = link.get('href', '') if isinstance(link, dict) else link
                if not (href.startswith('/docs/') or href == '/docs'):
                    continue
                ok_everywhere = True
                for brand in needed:
                    rel = href[len('/docs'):] or '/'
                    docs_path, frag = split_href(rel)
                    err = check_docs_link(brand, docs_path, frag, cache)
                    if err:
                        errors.append(f'{err} (features.json {feature.get("id")})')
                        ok_everywhere = False
                if ok_everywhere:
                    has_resolving_docs_link = True
            if needed and not has_resolving_docs_link:
                errors.append(f'features.json {feature.get("id")}: no /docs/ link resolving in every brand it is on ({", ".join(needed)})')

    shared_docs = [ROOT / 'internal/webassets/static/llms.txt']
    per_brand_docs = {
        'host': [ROOT / 'internal/webassets/static/llms.txt'],
        'hack': [ROOT / 'internal/webassets/static/hack-llms.txt'],
    }
    for brand in BRANDS:
        for doc in per_brand_docs.get(brand, []):
            if not doc.is_file():
                continue
            for href in re.findall(r'(?:href=["\']|\()([^\s"\'()]+)', doc.read_text()):
                rel = docs_relative(href, brand)
                if rel is None:
                    continue
                docs_path, frag = split_href(rel)
                err = check_docs_link(brand, docs_path, frag, cache)
                if err:
                    errors.append(f'{err} (from {doc.relative_to(ROOT)})')
        skills_dir = SKILLS_DIR.get(brand)
        if skills_dir and skills_dir.is_dir():
            for md in skills_dir.rglob('*.md'):
                for href in re.findall(r'(?:href=["\']|\]\(|\()([^\s"\')]+)', md.read_text()):
                    rel = docs_relative(href, brand)
                    if rel is None:
                        continue
                    docs_path, frag = split_href(rel)
                    err = check_docs_link(brand, docs_path, frag, cache)
                    if err:
                        errors.append(f'{err} (from {md.relative_to(ROOT)})')
    return errors


PHRASE_RE = re.compile(r'\bphrase\(\s*"((?:[^"\\]|\\.)*)"')


def limits_phrases():
    text = (ROOT / 'internal/handler/limitstext.go').read_text()
    return [json.loads('"' + m + '"') for m in PHRASE_RE.findall(text)]


def problems_limitstext():
    errors = []
    phrases = limits_phrases()
    mdx_files = sorted((SOURCE / 'src/content/docs').rglob('*.mdx'))
    for brand in BRANDS:
        tree = OUTPUT / brand
        if not tree.is_dir():
            continue
        built = '\n'.join(p.read_text() for p in tree.rglob('*.html'))
        source_text = ''
        for p in mdx_files:
            text = docs_site.brand_text(p.read_text(), brand)
            if text is not None:
                source_text += text
        for phrase in phrases:
            if phrase in source_text and phrase not in built:
                errors.append(f'{brand}: limitstext phrase missing from built HTML: {phrase!r}')
    return errors


def problems_hack_in_host():
    tree = OUTPUT / 'host'
    if not tree.is_dir():
        return ['host: no built tree, run scripts/build-docs-site.sh']
    errors = []
    for page in tree.rglob('*.html'):
        if re.search('hack', page.read_text(), re.I):
            errors.append(f'host build contains "hack": {page.relative_to(ROOT)}')
    return errors


def main():
    errors = []
    errors += problems_current()
    errors += problems_markers()
    errors += problems_links()
    errors += problems_limitstext()
    errors += problems_hack_in_host()
    if errors:
        for e in errors:
            print(e)
        return 1
    print('docs site: references, markers, links and limits text ok')
    return 0


if __name__ == '__main__':
    sys.exit(main())
