#!/usr/bin/env python3
"""Project the shared Hack navigation map into the browser's static data module."""
import argparse
import json
from functools import lru_cache
from pathlib import Path

root = Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument('--check', action='store_true')
args = parser.parse_args()
data = json.loads((root / 'internal/rules/hackevents/navigation.json').read_text())
ids = [t['id'] for t in data['targets']]
assert len(ids) == len(set(ids)), 'duplicate navigation target'
screens = {s['id']: s for s in data['screens']}
assert len(screens) == len(data['screens']), 'duplicate screen'
inventory = json.loads((root / 'docs/generated/inventory.json').read_text())
paths = {r['path'] for r in inventory['routes'] if r['method'] in ('GET', 'ANY')}
for screen in screens.values():
    if screen['path'].startswith('/'):
        assert screen['path'] in paths, 'screen missing from app route inventory: ' + screen['path']
for s in screens.values():
    if s['module']:
        assert (root / f"internal/webassets/static/hack/screens/{s['module']}.js").exists(), s
for t in data['targets']:
    assert t['screen'] in screens, t
    assert t['href_pattern'].split('?')[0].split('#')[0] == screens[t['screen']]['path'], t
for a in data['aliases']:
    assert a['target'] in ids, a
for r in data['roles']:
    if r['id'] == 'visitor':
        continue
    for n in r['nav']:
        assert n['target_id'] in ids, n
        assert any(s['role'] == r['id'] and s['section'] == n['id'] for s in screens.values()), n
# The explanation layer is deliberately separate from the browser projection.
# {slug} is the shared event address, resolved for every event link; other
# placeholders (notably {team} and {event_url}) are not assistant destinations.
import html
import re

GENERIC_BUTTONS = {
    'Save', 'Cancel', 'Copy', 'Copied', 'Close', 'Edit', 'Remove', 'Add',
    'Up', 'Down', 'Move up', 'Move down', 'Back', 'Done', 'Refresh',
    'Discard', 'Clear', 'Continue', 'Reload', 'Undo', 'Open', '×',
}


def static_parts(value):
    """Keep static template text, including text after nested ${expressions}."""
    parts, start, i = [], 0, 0
    while i < len(value):
        if value.startswith('${', i):
            parts.append(value[start:i])
            i = balanced_end(value, i + 1)
            start = i
        else:
            i += 1
    parts.append(value[start:])
    return parts


def balanced_end(value, start):
    """Position after balanced JS braces/parens, respecting quoted strings."""
    pairs = {'{': '}', '(': ')', '[': ']'}
    stack, i = [pairs[value[start]]], start + 1
    while i < len(value) and stack:
        c = value[i]
        if c in "'\"`":
            quote = c
            i += 1
            while i < len(value):
                if value[i] == '\\':
                    i += 2
                elif value[i] == quote:
                    i += 1
                    break
                elif quote == '`' and value.startswith('${', i):
                    i = balanced_end(value, i + 1)
                else:
                    i += 1
            continue
        if c in pairs:
            stack.append(pairs[c])
        elif c == stack[-1]:
            stack.pop()
        i += 1
    assert not stack, 'Unbalanced JavaScript near ' + value[start:start + 80]
    return i


def decoded(value):
    return html.unescape(value.replace("\\'", "'").replace('\\"', '"'))


def js_tokens(source):
    """Read strings/templates and comments, keeping offsets and template expressions.

    Comments inside strings (including URLs/HTML) are ordinary text. Template
    expressions are code, so their comments and nested strings are read too.
    """
    def code(i, in_expression=False):
        depth = 0
        while i < len(source):
            c = source[i]
            if source.startswith('//', i) or source.startswith('/*', i):
                end = source.find('\n', i) if source[i+1] == '/' else source.find('*/', i+2)
                end = len(source) if end < 0 else end + (2 if source[i+1] == '*' else 0)
                yield ('comment', i, end, '')
                i = end
                continue
            if c in "'\"`":
                start, quote = i, c
                i += 1
                while i < len(source):
                    if source[i] == '\\':
                        i += 2
                    elif source[i] == quote:
                        i += 1
                        break
                    elif quote == '`' and source.startswith('${', i):
                        nested = code(i+2, True)
                        try:
                            while True:
                                yield next(nested)
                        except StopIteration as end:
                            i = end.value
                    else:
                        i += 1
                yield ('string', start, i, source[start+1:i-1])
                continue
            # Regex literals can contain quotes or comment-like characters.
            prefix = source[:i].rstrip() if c == '/' else ''
            if c == '/' and (not prefix or prefix[-1] in '=(:,![&|?{;' or prefix.endswith('return')):
                j, bracket = i+1, False
                while j < len(source) and source[j] != '\n':
                    if source[j] == '\\':
                        j += 2
                        continue
                    if source[j] == '[':
                        bracket = True
                    elif source[j] == ']':
                        bracket = False
                    elif source[j] == '/' and not bracket:
                        j += 1
                        while j < len(source) and source[j].isalpha():
                            j += 1
                        break
                    j += 1
                i = j
                continue
            if in_expression:
                if c == '{':
                    depth += 1
                elif c == '}':
                    if depth == 0:
                        return i+1
                    depth -= 1
            i += 1
        return i
    yield from code(0)


def strip_js_comments(source):
    chars = list(source)
    for kind, start, end, _ in js_tokens(source):
        if kind == 'comment':
            chars[start:end] = ['\n' if c == '\n' else ' ' for c in source[start:end]]
    return ''.join(chars)


@lru_cache(maxsize=None)
def rendered_labels(source):
    """Exact text nodes in literal markup, or complete JS string labels.

    Standalone strings cover ui.el text, radio catalogues, dialog titles and
    dynamically assigned textContent. Never search arbitrary source substrings.
    """
    source = strip_js_comments(source)
    labels = []
    for kind, _, _, value in js_tokens(source):
        if kind != 'string':
            continue
        # Remove interpolation before parsing tags (expressions can contain >).
        parts = static_parts(value)
        value = '${…}'.join(parts)
        if re.search(r'</?[a-z][\w-]*\b', value, re.I):
            for attr in re.finditer(r'''\b(?:placeholder|aria-label|title)=([\"'])(.*?)\1''', value):
                labels.append(decoded(attr[2]))
            for node in re.split(r'<[^>]*>', value):
                label = ' '.join(decoded(node).split())
                if label:
                    labels.append(label)
        else:
            labels.append(' '.join(decoded(value).split()))
    return tuple(labels)


def label_present(label, source):
    label = ' '.join(decoded(label).split())
    for candidate in rendered_labels(source):
        if candidate == label:
            return True
        # Dynamic labels match only as whole labels, with all static fragments.
        for template, concrete in ((label, candidate), (candidate, label)):
            parts = static_parts(template)
            if len(parts) > 1 and any(p.strip() for p in parts):
                if re.fullmatch('.*?'.join(re.escape(p) for p in parts), concrete, re.S):
                    return True
    return False


def organiser_controls(source, include_options=False):
    """Scan literal markup and ui.el/el constructors, not a hand-kept ID list.

    Ignore comments only; conditional, hidden and disabled markup is scanned.
    JS interpolation is skipped as a balanced unit while reading HTML tags,
    so comparisons and quoted strings inside attributes do not truncate tags.
    Yields (offset, end-of-opening-tag, kind, attributes, visible static text).
    """
    source = strip_js_comments(source)
    for match in re.finditer(r'<([a-z][\w-]*)\b', source, re.I):
        i, quote = match.end(), None
        while i < len(source):
            if source.startswith('${', i):
                i = balanced_end(source, i + 1)
                continue
            c = source[i]
            if quote:
                if c == quote:
                    quote = None
            elif c in '\"\'':
                quote = c
            elif c == '>':
                break
            i += 1
        attrs = source[match.end():i]
        kind = match[1].lower()
        if kind not in (('input', 'select', 'textarea', 'button', 'option') if include_options else ('input', 'select', 'textarea', 'button')) and not re.search(r'\brole\s*=\s*[\"\'](?:radio|switch)[\"\']', attrs):
            continue
        visible = ''
        if kind in ('button', 'option'):
            close = source.find('</' + kind + '>', i)
            assert close >= 0, 'Unclosed button near ' + source[match.start():i]
            content = source[i+1:close]
            visible = ' '.join(re.sub(r'<[^>]*>', '', ('${…}'.join(static_parts(content)) if kind == 'option' else ''.join(static_parts(content)))).split())
            if kind == 'option' and not any(p.strip() for p in static_parts(content)):
                visible = ''
        yield match.start(), i, kind, attrs, decoded(visible)
    for match in re.finditer(r'\b(?:ui\.)?el\(\s*([\"\'])([a-z][\w-]*)\1\s*,\s*\{', source):
        end = balanced_end(source, match.end() - 1)
        attrs = source[match.end():end-1]
        if match[2] not in ('input', 'select', 'textarea', 'button') and control_attribute(attrs, 'role') not in ('radio', 'switch'):
            continue
        expression = object_field(attrs, 'text')
        choices = [value for kind, _, _, value in js_tokens(expression) if kind == 'string']
        if match[2] == 'button' and choices:
            for choice in choices:
                yield match.start(), end, match[2], attrs, decoded(choice)
        else:
            yield match.start(), end, match[2], attrs, ''



def object_field(attrs, name):
    """Read one top-level constructor property without swallowing later keys."""
    spans = {start: end for kind, start, end, _ in js_tokens(attrs) if kind == 'string'}
    i = 0
    while i < len(attrs):
        match = re.match(r"\s*(?:([a-zA-Z_$][\w$]*)|(['\"])(.*?)\2)\s*:\s*", attrs[i:], re.S)
        if not match:
            i += 1
            continue
        key = match[1] or match[3]
        start = i + match.end()
        i = start
        while i < len(attrs) and attrs[i] != ',':
            if i in spans:
                i = spans[i]
            elif attrs[i] in '{([':
                i = balanced_end(attrs, i)
            else:
                i += 1
        if key == name:
            return attrs[start:i].strip()
        i += 1
    return ''


def control_attribute(attrs, name):
    # HTML attributes and quoted/unquoted JS object keys.
    match = re.search(r'(?:^|[\s,])(?:' + re.escape(name) + r'|[\"\']' + re.escape(name) + r'[\"\'])\s*(?:=|:)\s*([\"\'`])(.*?)\1', attrs, re.S)
    return match[2] if match else None


def help_errors(help_map, navigation):
    errors, seen, targets_used, sources_used = [], set(), set(), set()
    records = help_map.get('records', [])
    records_by_id = {r['id']: r for r in records}
    targets = {t['id']: t for t in navigation['targets']}
    assets = root / 'internal/webassets/static/hack'
    for r in records:
        rid = r['id']
        if not rid or rid in seen:
            errors.append(f'help record {rid!r}: add a unique non-empty id')
        seen.add(rid)
        target = targets.get(r.get('target'))
        if not target or target['role'] != 'organiser' or '{' in target['href_pattern'].replace('{slug}', ''):
            errors.append(f'help record {rid}: target must be an existing organiser list/setting target (only shared {{slug}} is allowed)')
        if ('options' in r or r.get('target') not in targets_used) and not r.get('when', '').strip():
            errors.append(f'help record {rid}: options and first record for a target require non-empty when')
        targets_used.add(r.get('target'))
        for key in ('label', 'explains', 'sources'):
            if not r.get(key) or (isinstance(r[key], str) and not r[key].strip()):
                errors.append(f'help record {rid}: add non-empty {key}')
        texts = []
        for name in r.get('sources', []):
            path = assets / name
            if path.suffix != '.js' or not path.is_relative_to(assets) or not path.exists():
                errors.append(f'help record {rid}: missing JS source {name}')
            else:
                texts.append(path.read_text())
                sources_used.add(name)
        for label in [r.get('label', '')] + [o.get('label', '') for o in r.get('options', [])]:
            if not label or not any(label_present(label, text) for text in texts):
                errors.append(f'help record {rid}: label {label!r} missing from sources; use exact decoded UI text or add its source')
        for option in r.get('options', []):
            if not option.get('label', '').strip() or not option.get('means', '').strip():
                errors.append(f'help record {rid}: every option needs label and means')
        for related in r.get('related', []):
            if related not in records_by_id:
                errors.append(f'help record {rid}: related id {related!r} does not resolve')
    for target in navigation['targets']:
        if target['role'] == 'organiser' and target['kind'] in ('setting', 'section', 'action') and '{' not in target['href_pattern'].replace('{slug}', '') and target['id'] not in targets_used:
            errors.append(f'organiser target {target["id"]}: add a help record targeting it')
    # Detail/create screens use the nearest stable list target: their source
    # module establishes coverage without inventing parameterised links.
    for screen in navigation['screens']:
        if screen['role'] == 'organiser' and f'screens/{screen["module"]}.js' not in sources_used:
            errors.append(f'organiser screen {screen["id"]}: add a help record with its JS source')
    # Screen modules and their direct imports are the organiser dependency
    # boundary. Always include shared organisers' helpers even when an import
    # moves between screens. The assistant's own compose UI is claimed below.
    seeds = set(assets.glob('screens/organiser-*.js')) | set(assets.glob('screens/_*.js'))
    paths = {p for p in seeds if p.name.startswith('organiser-')}
    paths.update(assets / p for p in ('checklist.js', 'leave.js', 'stage-announce.js', 'schedule.js'))
    for path in seeds:
        source = strip_js_comments(path.read_text())
        imports = re.findall(r"\b(?:from\s*|import\s*(?:\(\s*)?)(['\"])([^'\"]+)\1", source)
        for _, name in imports:
            if name.startswith('.'):
                dependency = (path.parent / name).resolve()
                if dependency.suffix == '.js' and dependency.name != 'assistant.js':
                    paths.add(dependency)
    # Include helper screens reached directly and their control-bearing peers.
    paths.update(p for p in seeds if p.name.startswith(('_settings-', '_event-', '_team-message-', '_stage-announce-', '_org')))
    ask = records_by_id.get('assistant.ask', {})
    if ask.get('sources') != ['assistant.js'] or 'textarea:data-assistant-input' not in ask.get('controls', []):
        errors.append('assistant.ask: claim assistant.js and its chat textarea:data-assistant-input')
    elif not any(kind == 'textarea' and control_attribute(attrs, 'data-assistant-input') is not None
                 for _, _, kind, attrs, _ in organiser_controls((assets / 'assistant.js').read_text())):
        errors.append('assistant.ask: chat textarea claim no longer matches assistant.js')
    for path in sorted(paths):
        source = strip_js_comments(path.read_text())
        name = str(path.relative_to(assets))
        errors.extend(control_help_errors(source, name, records_by_id))
    return errors


def control_help_errors(source, name, records_by_id):
    errors = []
    local_records = [r for r in records_by_id.values() if name in r.get('sources', [])]
    for start, opening_end, kind, attrs, visible in organiser_controls(source):
        where = f'{name}:{source.count(chr(10), 0, start)+1}'
        help_id = control_attribute(attrs, 'data-help')
        control_id = control_attribute(attrs, 'id')
        claimed = [r for r in local_records if control_id and control_id in r.get('controls', [])]
        if help_id is not None:
            record = records_by_id.get(help_id)
            if record is None or name not in record.get('sources', []):
                errors.append(f'{where}: {kind} data-help={help_id!r}: record must exist and cite this source')
                claimed = []
            else:
                claimed = [record]
                if kind == 'button' and visible and record['label'] != visible and 'button:' + visible not in record.get('controls', []):
                    errors.append(f'{where}: button {visible!r}: data-help={help_id!r} must match its label or claim button:<text>')
        elif kind != 'button' or control_attribute(attrs, 'role') in ('radio', 'switch'):
            if not control_id or not ''.join(static_parts(control_id)).strip():
                errors.append(f'{where}: {kind} {control_id or "without id"}: add a static id registered in controls or data-help="<record id>"')
            elif not claimed:
                errors.append(f'{where}: {kind} id={control_id!r}: relevant help record must claim this control and source')
        elif not visible:
            expression = object_field(attrs, 'text')
            claim = 'button:text:' + expression if expression else ''
            if not claim or not any(claim in r.get('controls', []) for r in local_records):
                errors.append(f'{where}: dynamic button needs an explicit text-expression claim on a record citing this source')
        elif visible not in GENERIC_BUTTONS and not any(r['label'] == visible or 'button:' + visible in r.get('controls', []) for r in local_records):
            errors.append(f'{where}: button {visible!r}: add its label/"button:<text>" to a help record citing this source')
        if kind == 'select':
            close = source.find('</select>', opening_end)
            if close >= 0:
                for _, _, option_kind, _, label in organiser_controls(source[opening_end+1:close], include_options=True):
                    if option_kind != 'option':
                        continue
                    # Fully dynamic names (teams, countries, tracks) are not
                    # static choices; mixed static/dynamic labels are checked.
                    if not label.strip():
                        continue
                    if not any(label_present(label, '`' + o['label'] + '`') for r in claimed for o in r.get('options', [])):
                        errors.append(f'{where}: select {control_id or help_id!r}: option {label!r} needs a meaning on a record claiming this select and source')
    return errors


def check_help_scanner():
    fixture = """// <input id="comment-only">
    const text = `<input id="fixed"><input id="prefix-${row.id}">
      <textarea id="${dateID}" hidden></textarea>
      <button title="${a > b ? 'yes' : 'no'}">Save</button>
      <span role="radio" data-help="entry.evidence">Required</span>`
    ui.el('input', { type: 'checkbox' })
    el('textarea', { 'data-help': 'lock' })
    ui.el('span', { role: 'switch' })
    ui.el('button', { text: 'Generate assignments' })
    """
    controls = list(organiser_controls(fixture))
    assert len(controls) == 9, 'help scanner must include hidden markup, radios and el constructors'
    assert [control_attribute(c[3], 'id') for c in controls[:3]] == ['fixed', 'prefix-${row.id}', '${dateID}']
    assert ''.join(static_parts('${dateID}')) == '', 'fully dynamic ids need data-help'
    assert ''.join(static_parts('prefix-${row.id}')) == 'prefix-'
    assert controls[3][4] == 'Save'
    assert controls[-1][4] == 'Generate assignments'
    assert control_attribute(controls[-2][3], 'role') == 'switch'
    assert label_present('Entry & site', '`<label>Entry &amp; site</label>`')
    assert label_present('Email ${count} participants', '`Email ${people.length} participants`')
    assert not label_present('New unexplained setting', '<label>Existing setting</label>')

    # Inline/block comments and comments inside template expressions cannot
    # invent controls or keep a retired label alive. URLs/markers in strings
    # survive; regex literals cannot confuse the quote scanner.
    comments = r'''const x = 1; // <input id="retired"> Name
    const y = 2; /* <textarea id="retired-too"></textarea> Old label */
    const url = 'https://example.org/*keep*/';
    const escaped = 'it\'s // text';
    const regex = /['"/]/;
    const rendered = `<label>Name shown publicly</label>${1 /* Hidden label */}`;
    const end = '<input id="real">'; // <select id="fake"></select>
    '''
    assert [control_attribute(c[3], 'id') for c in organiser_controls(comments)] == ['real']
    assert not label_present('Name', comments)
    assert not label_present('Old label', comments)
    assert not label_present('Hidden label', comments)
    assert label_present('Name shown publicly', comments)
    assert label_present('https://example.org/*keep*/', comments)
    assert label_present("it's // text", comments)
    record = {'id': 'choice', 'label': 'Choose', 'sources': ['screen.js'],
              'controls': ['choice'], 'options': [{'label': 'First', 'means': 'First choice.'}]}
    records = {'choice': record}
    select = '<select id="choice"><option value="a">First</option></select>'
    assert not control_help_errors(select, 'screen.js', records)
    extra = select.replace('</select>', '<option value="b">New option</option></select>')
    assert any("option 'New option'" in e for e in control_help_errors(extra, 'screen.js', records))
    assert control_help_errors(select, 'other.js', records), 'control claims must cite their source'
    unrelated = dict(record, id='other', controls=['elsewhere'], options=[{'label': 'New option', 'means': 'Unrelated.'}])
    assert control_help_errors(extra, 'screen.js', dict(records, other=unrelated)), 'option claims must belong to this select'
    tagged = '<button data-help="choice">Delete everything</button>'
    assert control_help_errors(tagged, 'screen.js', records), 'data-help must not bypass action coverage'
    record['controls'].append('button:Delete everything')
    assert not control_help_errors(tagged, 'screen.js', records)
    assert not control_help_errors('<button data-help="choice">Choose</button>', 'screen.js', records)
    assert control_help_errors(tagged, 'other.js', records)
    assert object_field("text: 'Up', 'aria-label': 'Move up'", 'text') == "'Up'"


check_help_scanner()
help_map = json.loads((root / 'internal/rules/hackevents/navigation-help.json').read_text())
errors = help_errors(help_map, data)
if errors:
    raise SystemExit('Navigation help parity failed:\n- ' + '\n- '.join(errors))

spec_path = root / 'docs/ux-atlas/spec.json'
spec = json.loads(spec_path.read_text())
if not args.check:
    original = json.dumps(spec, sort_keys=True)
    for role in data['roles']:
        projection = next(r for r in spec['roles'] if r['id'] == role['id'])
        projection['nav'] = role['nav']
    settings_screen = next(s for s in spec['screens'] if s['id'] == 'organiser-settings')
    existing = {s['tab']: s for s in settings_screen['sub_pages']}
    settings_screen['sub_pages'] = [dict(existing.get(t['id'], {}), tab=t['id'], label=t['label'], url='?s=settings&tab=' + t['id']) for t in data['resolution']['settings_tabs']]
    if json.dumps(spec, sort_keys=True) != original:
        spec_path.write_text(json.dumps(spec, indent=2, ensure_ascii=False) + '\n')
for role in data['roles']:
    expected = next(r for r in spec['roles'] if r['id'] == role['id'])
    assert expected['nav'] == role['nav'], 'atlas role nav drift: ' + role['id']
settings = next(s for s in spec['screens'] if s['id'] == 'organiser-settings')['sub_pages']
assert data['resolution']['settings_tabs'] == [{'id': s['tab'], 'label': s['label']} for s in settings], 'atlas Settings projection drift'
text = '// Generated by scripts/build-hack-navigation.py; do not edit.\nexport const navigation = ' + json.dumps(data, indent=2, ensure_ascii=False) + '\n'
p = root / 'internal/webassets/static/hack/navigation-data.js'
if args.check:
    assert p.exists() and p.read_text() == text, 'stale navigation-data.js; run python3 scripts/build-hack-navigation.py'
else:
    p.write_text(text)
