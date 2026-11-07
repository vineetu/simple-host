#!/usr/bin/env python3
"""Prepare brand Markdown, externalize build scripts, and check docs offline."""
import hashlib
import json
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / 'docs/site'
OUTPUT = ROOT / 'internal/webassets/docs-site'
BRANDS = ('host', 'hack')


def sources():
    paths = [p for p in SOURCE.rglob('*') if p.is_file() and not any(
        x in p.relative_to(SOURCE).parts for x in ('node_modules', '.astro', '.generated-content', 'dist'))]
    paths += [ROOT / 'scripts/build-docs-site.sh', ROOT / 'scripts/docs-site.py']
    return sorted(paths)


def digest(paths, base):
    h = hashlib.sha256()
    for p in paths:
        h.update(str(p.relative_to(base)).encode() + b'\0' + p.read_bytes() + b'\0')
    return h.hexdigest()


def brand_text(text, brand):
    # Whole-page selection and non-nested conditional blocks, before MDX/TOC parsing.
    m = re.search(r'^brands: \[([^]]+)\]\s*$', text, re.M)
    if m and brand not in m[1].split(', '):
        return None
    text = re.sub(r'^brands: .*\n', '', text, flags=re.M)
    # Whole-line markers must not insert blank lines into Markdown tables.
    lines = r'^[ \t]*\{/\* brand:(host|hack) \*/\}\n(.*?)^[ \t]*\{/\* /brand \*/\}(?:\n|$)'
    text = re.sub(lines, lambda m: m[2] if m[1] == brand else '', text, flags=re.S | re.M)
    pattern = r'\{/\* brand:(host|hack) \*/\}(.*?)\{/\* /brand \*/\}'
    text = re.sub(pattern, lambda m: m[2] if m[1] == brand else '', text, flags=re.S)
    if '{/* brand:' in text or '{/* /brand */}' in text:
        raise ValueError('Unbalanced brand block')
    for key, value in {'PRODUCT': 'Simple Hack' if brand == 'hack' else 'Simple Host',
                       'DOMAIN': f'simple-{brand}.app',
                       'INSTALL': '/get-started' if brand == 'hack' else '/install.html',
                       'LLMS': '/llms.txt'}.items():
        text = text.replace('@' + key + '@', value)
    return text


def prepare(brand):
    target = SOURCE / '.generated-content'
    shutil.rmtree(target, ignore_errors=True)
    # Content stores are brand-specific; never reuse a previous brand's collection.
    shutil.rmtree(SOURCE / '.astro', ignore_errors=True)
    for p in sorted((SOURCE / 'src/content/docs').rglob('*.mdx')):
        text = brand_text(p.read_text(), brand)
        if text is not None:
            dest = target / p.relative_to(SOURCE / 'src/content/docs')
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_text(text)


SCRIPT = re.compile(r'<script\b([^>]*)>(.*?)</script>', re.S | re.I)
SRC = re.compile(r'\bsrc\s*=', re.I)


def externalize(brand):
    tree = OUTPUT / brand
    targets = json.loads((SOURCE / "old-anchors.json").read_text())[brand]
    script = (SOURCE / "public/old-anchors.js").read_text()
    (tree / "old-anchors.js").write_text(script.replace("@TARGETS@", json.dumps(targets, sort_keys=True)))
    for page in sorted(tree.rglob('*.html')):
        def replace(match):
            if SRC.search(match[1]):
                return match[0]
            body = match[2]
            name = hashlib.sha256(body.encode()).hexdigest()[:20] + '.js'
            dest = tree / '_scripts' / name
            dest.parent.mkdir(exist_ok=True)
            dest.write_text(body)
            return f'<script{match[1]} src="/docs/_scripts/{name}"></script>'
        body = SCRIPT.sub(replace, page.read_text())
        # Keep TOC links under the same runtime condition as their task heading.
        for kind in ('hosted', 'self'):
            blocks = re.findall(r'<!--sh:' + kind + r'-->(.*?)<!--/sh:' + kind + r'-->', body, re.S)
            for block in blocks:
                for heading in re.findall(r'<h2 id="([^"]+)"', block):
                    item = r'<li\b[^>]*>\s*<a href="#' + re.escape(heading) + r'"[^>]*>.*?</a>\s*</li>'
                    body = re.sub(item, lambda m: f'<!--sh:{kind}-->' + m[0] + f'<!--/sh:{kind}-->', body, flags=re.S)
        page.write_text(body)
    for p in tree.rglob('*.map'):
        p.unlink()


def record():
    manifest = {'sources': digest(sources(), ROOT), 'brands': {}}
    for brand in BRANDS:
        files = output_files(OUTPUT / brand)
        manifest['brands'][brand] = {'sha256': digest(files, OUTPUT), 'bytes': sum(p.stat().st_size for p in files)}
    (OUTPUT / 'manifest.json').write_text(json.dumps(manifest, indent=2) + '\n')


def output_files(tree):
    # Pagefind's pinned native binaries embed platform-specific WASM and indexes.
    # Keep both the digest and byte count reproducible across those platforms.
    return sorted(p for p in tree.rglob('*') if p.is_file()
                  and p.relative_to(tree).parts[0] != 'pagefind')


def check_pagefind(tree):
    index = tree / 'pagefind'
    if not index.is_dir():
        return [f'{tree.name}: missing pagefind directory']
    try:
        entry = json.loads((index / 'pagefind-entry.json').read_text())
        # Starlight marks each indexed HTML page with data-pagefind-body.
        pages = sum(bool(re.search(r'<[^>]+\bdata-pagefind-body(?:[\s=>])', p.read_text()))
                    for p in tree.rglob('*.html'))
        languages = entry['languages']
        if not languages or sum(lang['page_count'] for lang in languages.values()) != pages:
            return [f'{tree.name}: Pagefind page_count does not match {pages} indexed HTML pages']
        # These are the entry manifest's references, using Pagefind's filename
        # conventions. A null wasm uses the always-emitted unknown fallback.
        references = {'wasm.unknown.pagefind'}
        for lang in languages.values():
            references.add(f'pagefind.{lang["hash"]}.pf_meta')
            references.add(f'wasm.{lang["wasm"] or "unknown"}.pagefind')
        return [f'{tree.name}: Pagefind reference missing: {name}'
                for name in sorted(references) if not (index / name).is_file()]
    except (OSError, ValueError, KeyError, TypeError, AttributeError) as exc:
        return [f'{tree.name}: invalid pagefind-entry.json: {exc}']


def check():
    errors = []
    features = json.loads((ROOT / 'internal/webassets/static/features.json').read_text())
    ids = {f['id']: f for a in features['areas'] for f in a['features']}
    areas = json.loads((SOURCE / 'areas.json').read_text())
    for a in features['areas']:
        if a['id'] not in areas:
            errors.append(f'area {a["id"]} has no mapping')
    for p in (SOURCE / 'src/content/docs/guides').glob('*.mdx'):
        for brand in BRANDS:
            text = brand_text(p.read_text(), brand)
            if text is None:
                continue
            if p.stem not in areas or brand not in areas[p.stem]['brands']:
                errors.append(f'{p.name}: unmapped guide for {brand}')
            sections = re.split(r'^## ', text, flags=re.M)[1:]
            if len(sections) > 7:
                errors.append(f'{p.name}: more than seven H2s')
            for section in sections:
                title = section.splitlines()[0]
                if title in ('Errors you may see', 'Next'):
                    continue
                found = re.findall(r'<What id="([^"]+)"\s*/>', section)
                if len(found) != 1:
                    errors.append(f'{p.name} ({brand}) {title}: expected one What id')
                for fid in found:
                    if fid not in ids or ids[fid]['editions'][brand] not in ('yes', 'partial'):
                        errors.append(f'{p.name} ({brand}): {fid} is not on')
    try:
        manifest = json.loads((OUTPUT / 'manifest.json').read_text())
        if manifest['sources'] != digest(sources(), ROOT):
            errors.append('docs sources changed: run scripts/build-docs-site.sh')
        for brand in BRANDS:
            files = output_files(OUTPUT / brand)
            if not (OUTPUT / brand / 'index.html').is_file() or digest(files, OUTPUT) != manifest['brands'][brand]['sha256']:
                errors.append(f'{brand} docs output missing or changed: rebuild')
            errors.extend(check_pagefind(OUTPUT / brand))
            for page in (OUTPUT / brand).rglob('*.html'):
                body = page.read_text()
                for marker in ('head', 'header', 'footer'):
                    if body.count(f'<!--sh:{marker}-->') != 1:
                        errors.append(f'{page}: missing/duplicate {marker} marker')
                if '<starlight-tab-item' in body or '<p>|' in body:
                    errors.append(f'{page}: uncompiled tab or split Markdown table')
                if any(not SRC.search(m[1]) for m in SCRIPT.finditer(body)):
                    errors.append(f'{page}: inline script')
    except (FileNotFoundError, KeyError):
        errors.append('docs build missing: run scripts/build-docs-site.sh')
    if errors:
        raise SystemExit('\n'.join(errors))
    print('docs site source, feature links, chrome, CSP, output hashes, and Pagefind structure ok')


if __name__ == '__main__':
    command = sys.argv[1]
    if command == 'prepare':
        prepare(sys.argv[2])
    elif command == 'externalize':
        externalize(sys.argv[2])
    elif command == 'record':
        record()
    elif command == 'check':
        check()
    else:
        raise SystemExit('expected prepare, externalize, record, or check')
