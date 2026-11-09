#!/usr/bin/env python3
"""Generate the per-brand API and MCP tool reference pages from two Go commands:

  go run ./scripts/inventory tools-json -edition <brand>
  go run ./scripts/inventory openapi-json -edition <brand>

Output is plain Markdown (not MDX) under docs/site/.generated-content/<slug>/,
written by generate(brand, target) and wired into docs-site.py's prepare().
Deterministic: same inventory JSON in, same bytes out.
"""
import functools
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
# CORS preflight (OPTIONS) and the one bare HEAD mirror of a GET are not
# independently documented operations.
METHODS = ('get', 'post', 'put', 'patch', 'delete', 'trace')

FAMILY_LABELS = {
    'sites': 'Sites and versions',
    'access': 'Passcodes, viewers and domains',
    'storage': 'Storage',
    'accounts': 'Account',
    'imports': 'Imports and uploads',
    'notifications': 'Notifications',
    'discovery': 'Finding tools',
    'hack-events': 'Events, teams and members',
    'hack-entries': 'Entries, judging and results',
    'hack-app-only': 'App screens',
    'legacy': 'Older storage',
    'admin': 'Admin',
}


@functools.lru_cache(maxsize=None)
def inventory_text(command, brand):
    """One go run per command and brand per process (build and check share it)."""
    proc = subprocess.run(
        ['go', 'run', './scripts/inventory', command, '-edition', brand],
        cwd=ROOT, check=True, capture_output=True, text=True)
    return proc.stdout


def run_inventory(command, brand):
    return json.loads(inventory_text(command, brand))


def slugify(text):
    slug = re.sub(r'[^a-z0-9]+', '-', text.lower()).strip('-')
    return slug or 'x'


CODE_SPAN = re.compile(r'(`+)(.+?)\1', re.S)


def esc(text):
    """Escape raw < and > so plain Markdown never swallows them as HTML tags.
    Code spans already show them literally, so they are left alone."""
    if text is None:
        return ''
    out, last = [], 0
    for m in CODE_SPAN.finditer(text):
        out.append(text[last:m.start()].replace('<', '&lt;').replace('>', '&gt;'))
        out.append(m.group(0))
        last = m.end()
    out.append(text[last:].replace('<', '&lt;').replace('>', '&gt;'))
    return ''.join(out)


def cell(text):
    """Table-cell escaping: esc() plus the pipe that would split the row."""
    return esc(text).replace('|', '\\|').replace('\n', ' ')


def table(headers, rows):
    if not rows:
        return ''
    out = ['| ' + ' | '.join(headers) + ' |', '| ' + ' | '.join(['---'] * len(headers)) + ' |']
    for row in rows:
        out.append('| ' + ' | '.join(cell(c) for c in row) + ' |')
    return '\n'.join(out) + '\n'


def resolve_ref(spec, ref):
    if not ref.startswith('#/'):
        return None
    node = spec
    for part in ref[2:].split('/'):
        part = part.replace('~1', '/').replace('~0', '~')
        if not isinstance(node, dict) or part not in node:
            return None
        node = node[part]
    return node


def schema_type(schema, spec, depth=0):
    if not isinstance(schema, dict):
        return 'any'
    if '$ref' in schema:
        resolved = resolve_ref(spec, schema['$ref'])
        return schema_type(resolved, spec, depth) if resolved is not None else 'object'
    for combiner in ('oneOf', 'anyOf', 'allOf'):
        if combiner in schema:
            parts = [schema_type(s, spec, depth) for s in schema[combiner]]
            return ' or '.join(dict.fromkeys(parts))
    t = schema.get('type')
    if t == 'array':
        if depth >= 1:
            return 'array'
        return f"array of {schema_type(schema.get('items', {}), spec, depth + 1)}"
    if t == 'object' or (t is None and ('properties' in schema or 'additionalProperties' in schema)):
        return 'object'
    if isinstance(t, list):
        return ' or '.join(t)
    if schema.get('enum'):
        values = ', '.join(f'`{v}`' for v in schema['enum'])
        return f"{t or 'any'}, one of {values}"
    return t or 'any'


def mcp_param_rows(input_schema, spec=None, filt=None):
    props = (input_schema or {}).get('properties', {}) or {}
    required = set((input_schema or {}).get('required', []) or [])
    rows = []
    for name in sorted(props):
        prop = props[name] or {}
        desc = prop.get('description', '')
        if filt is not None:
            desc = filt(desc, name)
        rows.append((name, schema_type(prop, spec or {}), 'yes' if name in required else '', desc))
    return rows


def resolved_schema(schema, spec):
    if isinstance(schema, dict) and '$ref' in schema:
        resolved = resolve_ref(spec, schema['$ref'])
        return resolved if isinstance(resolved, dict) else {}
    return schema if isinstance(schema, dict) else {}


def request_body_section(op, spec, filt=None):
    body = op.get('requestBody')
    if not body:
        return ''
    body = resolved_schema(body, spec) if '$ref' in body else body
    content = (body or {}).get('content', {}) or {}
    if 'application/json' not in content:
        if not content:
            return ''
        media = sorted(content)[0]
        return f"**Request body:** {media}\n"
    schema = resolved_schema(content['application/json'].get('schema', {}), spec)
    rows = mcp_param_rows({'properties': schema.get('properties', {}), 'required': schema.get('required', [])}, spec, filt)
    if not rows:
        return ''
    return "**Request body:**\n\n" + table(['Name', 'Type', 'Required', 'Description'], rows)


def op_parameters(path_params, op, spec, filt=None):
    combined = []
    seen = set()
    for raw in list(path_params) + list(op.get('parameters', []) or []):
        param = resolve_ref(spec, raw['$ref']) if isinstance(raw, dict) and '$ref' in raw else raw
        if not isinstance(param, dict):
            continue
        key = (param.get('name'), param.get('in'))
        if key in seen:
            continue
        seen.add(key)
        combined.append(param)
    rows = []
    for param in combined:
        schema = param.get('schema', {}) or {}
        desc = param.get('description', '')
        if filt is not None:
            desc = filt(desc, param.get('name', ''))
        rows.append((param.get('name', ''), param.get('in', ''), schema_type(schema, spec),
                     'yes' if param.get('required') else '', desc))
    return rows


def auth_line(op):
    sec = op.get('security')
    if sec is None:
        return ''
    if sec == []:
        return 'No authentication required.'
    parts = []
    for alt in sec:
        for scheme, scopes in alt.items():
            if scheme == 'apiKey':
                parts.append('an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`)')
            elif scheme == 'connector':
                parts.append('a connector OAuth token' + (f" ({', '.join(scopes)} scope)" if scopes else ''))
            else:
                parts.append(scheme)
    return '**Auth:** ' + ' or '.join(dict.fromkeys(parts)) + '.'


def response_rows(op, spec, filt=None):
    rows = []
    for status, resp in (op.get('responses', {}) or {}).items():
        resp = resolve_ref(spec, resp['$ref']) if isinstance(resp, dict) and '$ref' in resp else resp
        desc = (resp or {}).get('description', '')
        if filt is not None:
            desc = filt(desc, status)
        rows.append((status, desc))
    return rows


def operation_entries(spec):
    """Every operation in the spec's own path/method order, grouped by tag."""
    entries = []
    for path, item in spec.get('paths', {}).items():
        shared_params = item.get('parameters', []) or []
        for method in METHODS:
            if method not in item:
                continue
            op = item[method]
            if not isinstance(op, dict):
                continue
            tags = op.get('tags') or []
            tag = tags[0] if tags else None
            entries.append((tag, path, method, op, shared_params))
    return entries


def api_pages(spec, hack_filter=None):
    tags = spec.get('tags', []) or []
    entries = operation_entries(spec)
    by_tag = {}
    for tag, path, method, op, shared_params in entries:
        by_tag.setdefault(tag, []).append((path, method, op, shared_params))

    pages = {}
    index_rows = []
    for tag_obj in tags:
        name = tag_obj.get('name', '')
        ops = by_tag.get(name, [])
        if not ops:
            continue
        slug = slugify(name)
        if slug in pages:
            raise ValueError(f'two API tags share the page slug {slug!r}')
        tag_description = tag_obj.get('description', '')
        if hack_filter is not None:
            tag_description = hack_filter(tag_description, f'tag {name}')
        index_rows.append((f'[{name}](/docs/api/{slug}/)', tag_description, str(len(ops))))
        lines = [esc(tag_description), '']
        for path, method, op, shared_params in ops:
            heading = f'{method.upper()} {path}'
            lines.append(f'## {heading}')
            lines.append('')
            lines.append(f'<div data-operation="{heading}"></div>')
            lines.append('')
            if op.get('deprecated'):
                lines.append('**Deprecated.**')
                lines.append('')
            description = op.get('description') or op.get('summary') or ''
            if hack_filter is not None:
                description = hack_filter(description, heading)
            lines.append(esc(description))
            lines.append('')
            auth = auth_line(op)
            if auth:
                lines.append(auth)
                lines.append('')
            param_rows = op_parameters(shared_params, op, spec, hack_filter)
            if param_rows:
                lines.append('**Parameters:**')
                lines.append('')
                lines.append(table(['Name', 'In', 'Type', 'Required', 'Description'], param_rows))
            body_section = request_body_section(op, spec, hack_filter)
            if body_section:
                lines.append(body_section)
            resp_rows = response_rows(op, spec, hack_filter)
            if resp_rows:
                lines.append('**Responses:**')
                lines.append('')
                lines.append(table(['Status', 'Description'], resp_rows))
        pages[slug] = (name, '\n'.join(lines).rstrip() + '\n')
    index = [
        'Read the whole contract as [OpenAPI JSON](/openapi.json) or [OpenAPI YAML](/openapi.yaml); '
        'the areas below link to the same operations as readable pages.', '',
        table(['Area', 'Description', 'Operations'], index_rows),
    ]
    return '\n'.join(index).rstrip() + '\n', pages


def mcp_pages(tools, install_href, hack_filter=None):
    by_family = {}
    for tool in tools:
        family = tool['family']
        if family not in FAMILY_LABELS:
            raise ValueError(f'unknown MCP tool family: {family!r} (tool {tool["name"]})')
        by_family.setdefault(family, []).append(tool)

    pages = {}
    index_rows = []
    for family in FAMILY_LABELS:
        tools_in_family = by_family.get(family)
        if not tools_in_family:
            continue
        label = FAMILY_LABELS[family]
        index_rows.append((f'[{label}](/docs/mcp/{family}/)', str(len(tools_in_family))))
        lines = []
        for tool in tools_in_family:
            lines.append(f'## {tool["name"]}')
            lines.append('')
            lines.append(f'<div data-tool="{tool["name"]}"></div>')
            lines.append('')
            lines.append(esc(tool.get('description', '')))
            lines.append('')
            rows = mcp_param_rows(tool.get('input_schema'), filt=hack_filter)
            if rows:
                lines.append('**Parameters:**')
                lines.append('')
                lines.append(table(['Name', 'Type', 'Required', 'Description'], rows))
            if not tool.get('listed', True):
                lines.append('Reached through find_tools and run_tool.')
                lines.append('')
        pages[family] = (label, '\n'.join(lines).rstrip() + '\n')
    index = [
        f'Connect an agent first — see the [connect guide]({install_href}).', '',
        table(['Family', 'Tools'], index_rows),
    ]
    return '\n'.join(index).rstrip() + '\n', pages


# ── Host owner constraint: public Simple Host output must never say "hack" ──
HACK_WORD = re.compile(r'hack', re.I)
PARENS = re.compile(r'\s*\([^()]*\)')
SENTENCE_SPLIT = re.compile(r'(?<=[.!?])\s+')
CONJUNCTION = re.compile(r'\s*(?:,\s*)?(?:and|or|&)\s+(?:on\s+)?Simple Hack\b', re.I)


def drop_hack_clauses(sentence):
    """Within one sentence, a Hack clause and the clauses after it (split on
    ';') are about Hack; the clauses before it still describe Host."""
    clauses = sentence.split('; ')
    for i, clause in enumerate(clauses):
        if HACK_WORD.search(clause):
            kept = '; '.join(clauses[:i]).rstrip(' ,:;')
            if not kept:
                return ''
            return kept if kept[-1] in '.!?' else kept + '.'
    return sentence


def drop_hack_mentions(text, where):
    if not text or not HACK_WORD.search(text):
        return text
    text = PARENS.sub(lambda m: '' if HACK_WORD.search(m.group(0)) else m.group(0), text)
    text = re.sub(r'[ \t]{2,}', ' ', text).strip()
    if not HACK_WORD.search(text):
        return text
    # A sentence naming both products as a conjunction ("... on Simple Host
    # and Simple Hack.") is about Host too; drop just the Hack half.
    trimmed = CONJUNCTION.sub('', text)
    if not HACK_WORD.search(trimmed):
        return trimmed.strip()
    kept = [c for c in (drop_hack_clauses(s) for s in SENTENCE_SPLIT.split(trimmed)) if c]
    if not kept:
        raise ValueError(f'host description would vanish entirely after the Hack filter: {where}')
    return ' '.join(kept).strip()


def generate(brand, generated_content):
    """Write the generated reference pages for brand under generated_content
    (docs/site/.generated-content), and the page-list manifest reference.json
    that astro.config.mjs reads for the sidebar."""
    tools = run_inventory('tools-json', brand)
    spec = run_inventory('openapi-json', brand)

    hack_filter = drop_hack_mentions if brand == 'host' else None
    api_index, api_tag_pages = api_pages(spec, hack_filter)
    if brand == 'host':
        for tool in tools:
            tool['description'] = drop_hack_mentions(tool.get('description', ''), tool['name'])
            if tool.get('title'):
                tool['title'] = drop_hack_mentions(tool['title'], tool['name'] + ' title')

    install_href = '/get-started' if brand == 'hack' else '/install.html'
    mcp_index, mcp_family_pages = mcp_pages(tools, install_href, hack_filter)

    api_dir = generated_content / 'api'
    mcp_dir = generated_content / 'mcp'
    api_dir.mkdir(parents=True, exist_ok=True)
    mcp_dir.mkdir(parents=True, exist_ok=True)
    (api_dir / 'index.md').write_text(front_matter('API reference', 'api') + api_index)
    (mcp_dir / 'index.md').write_text(front_matter('MCP tools', 'mcp') + mcp_index)

    api_entries = [{'slug': 'api', 'title': 'API reference'}]
    mcp_entries = [{'slug': 'mcp', 'title': 'MCP tools'}]
    for slug in sorted(api_tag_pages):
        title, body = api_tag_pages[slug]
        page_slug = f'api/{slug}'
        (api_dir / f'{slug}.md').write_text(front_matter(title, page_slug) + body)
        api_entries.append({'slug': page_slug, 'title': title})
    for family in FAMILY_LABELS:
        if family not in mcp_family_pages:
            continue
        title, body = mcp_family_pages[family]
        page_slug = f'mcp/{family}'
        (mcp_dir / f'{family}.md').write_text(front_matter(title, page_slug) + body)
        mcp_entries.append({'slug': page_slug, 'title': title})

    if brand == 'host':
        combined = api_index + '\n'.join(p[1] for p in api_tag_pages.values())
        combined += mcp_index + '\n'.join(p[1] for p in mcp_family_pages.values())
        if HACK_WORD.search(combined):
            raise ValueError('host reference output still mentions "hack"')

    manifest = {'api': api_entries, 'mcp': mcp_entries}
    (generated_content / 'reference.json').write_text(json.dumps(manifest, indent=2) + '\n')


def front_matter(title, slug):
    return '\n'.join(['---', f'title: {json.dumps(title)}', f'slug: {json.dumps(slug)}',
                       '---', '']) + '\n'


if __name__ == '__main__':
    import sys
    brand = sys.argv[1]
    target = Path(sys.argv[2])
    generate(brand, target)
