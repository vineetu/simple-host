#!/usr/bin/env bash
set -euo pipefail
# Usage: bash scripts/split/move.sh [--map FILE] [--dry-run] [--text GIT-PATHSPEC]...
# Map: whitespace-separated old-path new-path, # comments; one * in the last
# old component, substituted into new * or appended to a trailing new /.
# Rule 1: Source exists, destination absent: git mv.
# Rule 2: Source absent, destination exists: repair references only.
# Rule 3: Both exist: merge files, remove identical tracked twins, reject collisions.
# Rule 4: Directory merges preserve existing destination files.
# Rule 5: Protect nested modules; explicit whole-module moves repair root replace.
# Rule 6: Longest literal row wins; * matches zero or more component characters.
# Rule 7: Rewrite dynamic keys and require their resulting regular files to exist.
# Rule 8: Repeating the same map leaves the resulting tree unchanged.
# Text defaults below include docs/ux-atlas/build.py. D3-S7 must pass --text for
# reviewed deployment inputs, e.g. deploy/hack/standalone/Dockerfile,
# deploy/hack/setup-instance.sh, deploy/digitalocean/droplet/test/pins_test.sh.
# Under a whole-root rename, bare directory names in other files need hand edits
# for that slice (known: the scan tuple ('internal', 'cmd', 'products') in
# scripts/check-openapi-coverage.py). Go files outside the text list with
# root-relative path strings need --text (known: internal/brain/build.go and
# internal/brain/brain_test.go); the leftover report lists these for review.
# The leftover report lists remaining old path references for human review.
python3 - "$@" <<'PY'
import argparse
import difflib
import glob
import json
import os
import pathlib
import posixpath as P
import re
import subprocess
import sys
from dataclasses import dataclass, field

ROOT_NAME_FILES = ['scripts/inventory/main.go']
TEXT_PATHS = ['Makefile', 'Dockerfile', '.github/workflows/*', 'CLAUDE.md',
              'scripts/**', 'docs/ux-atlas/build.py']
TOKEN = re.compile(r'(?:[A-Za-z0-9_./*@+-]+|\{[A-Za-z0-9_./*,@+-]+\})+')
ROOT_EXPRESSION = re.compile(
    r'(?<![A-Za-z0-9_./-])'
    r'(\$(?:(?i:\{(?:ROOT|REPO|REPO_ROOT|REPO_DIR|TOP|TOPLEVEL|GITHUB_WORKSPACE|PWD)\}'
    r'|(?:ROOT|REPO|REPO_ROOT|REPO_DIR|TOP|TOPLEVEL|GITHUB_WORKSPACE|PWD)\b)'
    r'|\(pwd\)|\(git rev-parse --show-toplevel\)))/'
    r'([A-Za-z0-9_./*@+-]+)')
# Unrecognized standalone variable prefixes are unresolved references for review.
REPORT_VARIABLE = re.compile(
    r'(?<![A-Za-z0-9_./-])\$(?:\{[A-Za-z_][A-Za-z_0-9]*\}'
    r'|[A-Za-z_][A-Za-z_0-9]*|\(dirname [^\n]*?\))/([A-Za-z0-9_./*@+-]+)')


def git(args):
    # Rules 1-8: read the selected repository without assuming a clean tree.
    return subprocess.check_output(['git', *args]).decode().rstrip('\n')


def git_files(args):
    # Rules 1-4: preserve whitespace in index pathnames.
    return [p for p in git([args[0], '-z', *args[1:]]).split('\0') if p]


def parse_map(filename, errors):
    # Rule 6: validate rows and order by literal specificity.
    rows = []
    seen = set()
    for number, line in enumerate(pathlib.Path(filename).read_text().splitlines(), 1):
        bits = line.split('#', 1)[0].split()
        if not bits:
            continue
        if len(bits) != 2:
            errors.append(f'map line {number}: expected two paths')
            continue
        old, new = bits
        invalid = any(p.startswith(('/', './')) or '..' in p.split('/') for p in bits)
        invalid |= old in seen or old.count('*') > 1 or '*' in P.dirname(old)
        invalid |= new.count('*') > 1 or ('*' in new and '*' not in old)
        invalid |= '*' in old and '*' not in new and not new.endswith('/')
        if invalid:
            errors.append(f'map line {number}: malformed or duplicate row')
            continue
        seen.add(old)
        rows.append((old, new))
    return sorted(rows, key=lambda row: -len(row[0].replace('*', '')))


def discover_modules(root):
    # Rule 5: stop at nested go.mod boundaries.
    nested = {}
    for base, dirs, files in os.walk(root):
        dirs[:] = [d for d in dirs if d != '.git']
        directory = P.relpath(base, root)
        if directory != '.' and 'go.mod' in files:
            text = pathlib.Path(base, 'go.mod').read_text()
            nested[directory] = re.search(r'^module\s+(\S+)', text, re.M)[1]
            dirs[:] = []
    return nested


def module_owner(path, nested):
    # Rule 5: classify both old and new file locations.
    return next((d for d in nested if path == d or path.startswith(d + '/')), None)


def map_path(path, rows, symbol=False):
    # Rules 2,6,8: mapping depends only on the map, never on prior operations.
    for old, new in rows:
        if '*' in old:
            if not path.startswith(old.split('*', 1)[0]):
                continue
            pattern = re.escape(old).replace(r'\*', '([^/]*)')
            match = re.match('^' + pattern + r'(?=$|/)', path)
            if match:
                head = match[0]
                destination = new.replace('*', match[1]) if '*' in new else new + P.basename(head)
                return destination + path[len(head):]
        elif path == old or path.startswith(old + '/'):
            return new.rstrip('/') + path[len(old):]
        elif symbol and re.fullmatch(re.escape(old) + r'\.[A-Za-z_]\w*', path):
            return new.rstrip('/') + path[len(old):]
    return path


def whole_roots(rows):
    # Rules 2,6,8: a B/* N/* row renames the directory consumers too.
    roots = {}
    for old, new in rows:
        if old.endswith('/*') and new.endswith('/*'):
            roots[old[:-2]] = new[:-2]
    return roots


@dataclass
class Plan:
    moves: dict = field(default_factory=dict)
    removals: dict = field(default_factory=dict)
    units: dict = field(default_factory=dict)
    replacements: dict = field(default_factory=dict)
    rewrites: dict = field(default_factory=dict)
    gofiles: list = field(default_factory=list)


def plan_operations(tracked, rows, nested, errors):
    # Rules 1-6: plan every move/removal and all predictable destination conflicts.
    plan = Plan()
    for old, new in rows:
        if '*' in old:
            continue
        destination = new.rstrip('/')
        if old in nested:
            plan.units[old] = destination
            plan.replacements[old] = destination
            if os.path.lexists(destination) and old != destination:
                errors.append(f'unsupported whole-module destination exists: {old} -> {destination}')
        elif not os.path.exists(old) and destination in nested:
            plan.replacements[old] = destination
    destination_modules = dict(nested)
    for source, destination in plan.units.items():
        destination_modules[destination] = nested[source]
    owners = {}
    for source in tracked:
        owner = module_owner(source, nested)
        if owner in plan.units:
            destination = plan.units[owner] + source[len(owner):]
        elif owner:
            continue
        else:
            destination = map_path(source, rows)
        if destination == source or not os.path.isfile(source):
            continue
        target_owner = module_owner(destination, destination_modules)
        if target_owner and owner not in plan.units:
            errors.append(f'collision: destination inside nested module: {source} -> {destination}')
        if destination in owners:
            errors.append(f'duplicate destination: {owners[destination]}, {source} -> {destination}')
        owners[destination] = source
        ancestor = P.dirname(destination)
        while ancestor:
            if os.path.lexists(ancestor) and not os.path.isdir(ancestor):
                errors.append(f'collision: destination ancestor is a file: {ancestor}')
            ancestor = P.dirname(ancestor)
        if os.path.lexists(destination):
            if destination not in tracked and not os.path.isdir(destination):
                errors.append(f'untracked file at destination: {source} -> {destination}')
            elif not os.path.isfile(destination):
                errors.append(f'collision: destination is a directory: {source} -> {destination}')
            elif pathlib.Path(source).read_bytes() != pathlib.Path(destination).read_bytes():
                errors.append(f'collision: {source} -> {destination}')
            else:
                plan.removals[source] = destination
        else:
            plan.moves[source] = destination
    for destination in owners:
        ancestor = P.dirname(destination)
        while ancestor:
            if ancestor in owners:
                errors.append(f'collision: planned file is destination ancestor: {ancestor} -> {destination}')
            ancestor = P.dirname(ancestor)
    return plan


def future_files(plan):
    # Rule 7: use existing regular files, including untracked files, minus removals.
    files = set()
    for base, dirs, names in os.walk('.'):
        dirs[:] = [d for d in dirs if d != '.git']
        for name in names:
            path = P.normpath(P.join(base, name))
            if os.path.isfile(path):
                files.add(path)
    return (files - plan.moves.keys() - plan.removals.keys()) | set(plan.moves.values())


def go_spans(text):
    # Rules 1,2,8: scan once; quotes in comments and rune literals are not strings.
    i = 0
    while i < len(text):
        if text.startswith('//', i):
            end = text.find('\n', i)
            end = len(text) if end < 0 else end
            yield 'line_comment', i, end, text[i:end]
            i = end
        elif text.startswith('/*', i):
            end = text.find('*/', i + 2)
            end = len(text) if end < 0 else end + 2
            yield 'block_comment', i, end, text[i:end]
            i = end
        elif text[i] in ('"', "'", '`'):
            start = i
            quote = text[i]
            i += 1
            while i < len(text):
                if text[i] == quote:
                    i += 1
                    break
                if text[i] == '\\' and quote != '`':
                    i += 1
                i += 1
            if quote != "'":
                yield 'string', start, i, text[start:i]
        else:
            i += 1


def escaped_go(literal):
    # Escapes beyond backslash and quote require a human edit, never re-encoding.
    return literal.startswith('"') and any(m[0] not in (r'\\', r'\"') for m in re.finditer(r'\\.', literal[1:-1]))


def unquote_go(literal):
    # Rules 1,2: decode Go interpreted/raw strings for reference grammars.
    if literal.startswith('`'):
        return literal[1:-1].replace('\r', '')
    return re.sub(r'\\([\\"])', lambda match: match[1], literal[1:-1])


def quote_go(value, original):
    # Rule 8: preserve literal style where possible.
    if original.startswith('`') and '`' not in value:
        return '`' + value + '`'
    return json.dumps(value, ensure_ascii=False)


def rewrite_import(value, module, nested, rows):
    # Rules 2,5,6: exact module-prefixed string values; nested identities stay fixed.
    if any(value == m or value.startswith(m + '/') for m in nested.values()):
        return value
    if value.startswith(module + '/'):
        return module + '/' + map_path(value[len(module) + 1:], rows)
    return value


def rewrite_relative(value, source, destination, rows, future):
    # Rules 1,2: resolve relative literals from their source location, including twins.
    if not value.startswith(('./', '../')):
        return value
    leading = re.split(r'[*?\[]', value, 1)[0].rstrip('/')
    target = P.normpath(P.join(P.dirname(source), leading))
    full = P.normpath(P.join(P.dirname(source), value))
    mapped = map_path(full, rows)
    mapped_target = map_path(target, rows)
    if not os.path.exists(target):
        exists = (os.path.exists(mapped_target) or mapped_target in future
                  or any(path.startswith(mapped_target.rstrip('/') + '/') for path in future))
        if mapped == full or not exists:
            return value
    if mapped == full and P.dirname(source) == P.dirname(destination):
        return value
    result = P.relpath(mapped, P.dirname(destination) or '.')
    if value.endswith('/'):
        result += '/'
    if value.startswith('./') and not result.startswith('.'):
        result = './' + result
    return result


def rewrite_go_literals(text, source, destination, module, nested, rows, future):
    # Rules 1,2,5: edit string spans without interpreting comments as Go source.
    edits = []
    for kind, start, end, literal in go_spans(text):
        if kind != 'string':
            continue
        value = unquote_go(literal)
        if escaped_go(literal):
            # Decide from the path prefix before the first unsupported escape.
            prefix = re.split(r'\\(?![\\"])', value, 1)[0]
            mapped = rewrite_import(prefix, module, nested, rows)
            if mapped == prefix:
                mapped = rewrite_relative(prefix, source, destination, rows, future)
            if mapped != prefix:
                print(f'REVIEW escaped literal {source}: {literal}')
            continue
        mapped = rewrite_import(value, module, nested, rows)
        if mapped == value:
            mapped = rewrite_relative(value, source, destination, rows, future)
        if mapped != value:
            edits.append((start, end, quote_go(mapped, literal)))
    for start, end, replacement in reversed(edits):
        text = text[:start] + replacement + text[end:]
    return text


def rewrite_embeds(text, source, destination, rows, errors):
    # Rules 1,2: parse only directive lines using Go double/raw quoting.
    def directive(match):
        # Rules 1,2: preserve every unaffected embed pattern span.
        body = match[2]
        edits = []
        for token in re.finditer(r'"(?:\\.|[^"\\])*"|`[^`]*`|[^\s]+', body):
            original = token[0]
            value = unquote_go(original) if original[0] in ('"', '`') else original
            prefix = 'all:' if value.startswith('all:') else ''
            pattern = value[len(prefix):]
            target = P.normpath(P.join(P.dirname(source), pattern))
            mapped = map_path(target, rows)
            if mapped == target and P.dirname(source) == P.dirname(destination):
                continue
            if escaped_go(original):
                print(f'REVIEW escaped literal {source}: {original}')
                continue
            relative = P.relpath(mapped, P.dirname(destination) or '.')
            if relative == '..' or relative.startswith('../'):
                errors.append(f'embed leaves package: {source}: {pattern} -> {relative}')
            value = prefix + relative
            replacement = quote_go(value, original) if original[0] in ('"', '`') else value
            if any(c.isspace() for c in value) and original[0] not in ('"', '`'):
                replacement = json.dumps(value)
            edits.append((token.start(), token.end(), replacement))
        for start, end, replacement in reversed(edits):
            body = body[:start] + replacement + body[end:]
        return match[1] + body
    edits = []
    for kind, start, end, comment in go_spans(text):
        if kind != 'line_comment' or text[text.rfind('\n', 0, start) + 1:start].strip():
            continue
        match = re.fullmatch(r'(//go:embed[ \t]+)([^\n]+)', comment)
        if match:
            edits.append((start, end, directive(match)))
    for start, end, replacement in reversed(edits):
        text = text[:start] + replacement + text[end:]
    return text


def expand_braces(token):
    # Rule 6: expand alternatives for text and layering grammars.
    match = re.search(r'\{([^{}]+)\}', token)
    if not match:
        return [token]
    return [item for choice in match[1].split(',')
            for item in expand_braces(token[:match.start()] + choice + token[match.end():])]


def map_text_token(token, source, destination, module, nested, rows, roots, future, quoted=False):
    # Rules 2,5,6,8: root-relative tokens only, with map-derived whole-root consumers.
    if '{' in token:
        choices = expand_braces(token)
        mapped = [map_text_token(c, source, destination, module, nested, rows, roots, future, quoted) for c in choices]
        if choices == mapped:
            return token
        changes = []
        for before, after in zip(choices, mapped):
            suffix = os.path.commonprefix([before[::-1], after[::-1]])[::-1]
            changes.append((before[:-len(suffix)], after[:-len(suffix)]) if suffix else (before, after))
        if len(set(changes)) == 1 and token.startswith(changes[0][0]):
            before, after = changes[0]
            return after + token[len(before):]
        print(f'REVIEW brace token {source}: {token}')
        return token
    if any(token == m or token.startswith(m + '/') for m in nested.values()):
        return token
    if token.startswith('../'):
        return token if source.endswith('.go') else rewrite_relative(token, source, destination, rows, future)
    prefix = ''
    if token.startswith(module + '/'):
        prefix = module + '/'
    elif token.startswith('./'):
        relative = rewrite_relative(token, source, destination, rows, future) if not source.endswith('.go') else token
        if relative != token:
            return relative
        prefix = './'
    core = token[len(prefix):]
    bare = core.rstrip('/')
    if bare in roots and (core.endswith('/') or prefix or (quoted and source in ROOT_NAME_FILES)):
        return prefix + roots[bare] + ('/' if core.endswith('/') else '')
    for old, new in rows:
        if '/' not in old and not prefix and (core == old or core.startswith(old + '.')):
            return token
    return prefix + map_path(core, rows, symbol=True)


def rewrite_text(text, source, destination, module, nested, rows, roots, future):
    # Rules 1,2,8: shell roots are allowlisted and must start a path expression.
    if source != destination:
        def discovery(match):
            # Rules 1,2: re-relativise a moved script's root discovery.
            target = P.normpath(P.join(P.dirname(source), match[2].lstrip('/')))
            if not os.path.exists(target):
                return match[0]
            relative = P.relpath(map_path(target, rows), P.dirname(destination) or '.')
            return match[1] + '/' + relative
        text = re.sub(r'(\$\(dirname [^\n]*?\))(/(?:\.\./)*\.\.)', discovery, text)
    def protected_spans(value):
        # Text opt-ins must also preserve escaped strings and example directives.
        if not source.endswith('.go'):
            return []
        return [(start, end, literal) for kind, start, end, literal in go_spans(value)
                if (kind == 'string' and escaped_go(literal)) or
                (kind in ('string', 'block_comment') and
                 re.search(r'(?m)^[ \t]*//go:embed\b', literal))]
    protected = protected_spans(text)
    def root_reference(match):
        # Rules 2,8: root expressions resolve from the repository root.
        value = map_text_token(module + '/' + match[2], source, destination, module, nested, rows, roots, future)
        replacement = match[1] + '/' + value.removeprefix(module + '/')
        for begin, finish, literal in protected:
            if begin <= match.start() < finish:
                if replacement != match[0] and escaped_go(literal):
                    print(f'REVIEW escaped literal {source}: {literal}')
                return match[0]
        return replacement
    text = ROOT_EXPRESSION.sub(root_reference, text)
    protected = protected_spans(text)
    reviewed = set()
    def token_reference(match):
        # Rules 2,8: bare directory names require whole-string quoting.
        start, end = match.span()
        quoted = start > 0 and end < len(text) and text[start - 1] in ('"', "'", '`') and text[end] == text[start - 1]
        mapped = map_text_token(match[0], source, destination, module, nested, rows, roots, future, quoted)
        for begin, finish, literal in protected:
            if begin <= start < finish:
                if mapped != match[0] and begin not in reviewed and escaped_go(literal):
                    print(f'REVIEW escaped literal {source}: {literal}')
                    reviewed.add(begin)
                return match[0]
        return mapped
    return TOKEN.sub(token_reference, text)


def layer_pattern(pattern, rows, base):
    # Rules 2,6,8: relative rows read at internal; explicit @/ stays explicit.
    negative = '!' if pattern.startswith('!') else ''
    core = pattern[len(negative):]
    if core in ('-', '/**', '@/**'):
        return pattern
    suffix = '/**' if core.endswith('/**') else ''
    core = core[:-3] if suffix else core
    explicit = core.startswith('@/')
    root = core[2:] if explicit else 'internal/' + core
    mapped = map_path(root, rows)
    result = '@/' + mapped
    if not explicit and mapped.startswith(base + '/'):
        result = mapped[len(base) + 1:]
    return negative + result + suffix


def layer_alternatives(token, rows, base):
    # Rules 2,6: keep unchanged brace alternatives grouped in their original order.
    choices = expand_braces(token)
    unchanged = [c for c in choices if layer_pattern(c, rows, base) == c]
    changed = [layer_pattern(c, rows, base) for c in choices if layer_pattern(c, rows, base) != c]
    if not changed:
        return [token]
    keep = []
    if len(unchanged) == 1:
        keep = unchanged
    elif unchanged:
        brace = re.fullmatch(r'([^{}]*)\{([^{}]+)\}([^{}]*)', token)
        pre = brace[1] if brace else os.path.commonprefix(unchanged)
        post = brace[3] if brace else os.path.commonprefix([c[::-1] for c in unchanged])[::-1]
        if len(pre) + len(post) > min(map(len, unchanged)):
            post = ''
        keep = [pre + '{' + ','.join(c[len(pre):len(c) - len(post) if post else None] for c in unchanged) + '}' + post]
    return keep + changed


def rewrite_layering(text, rows, base):
    # Rules 2,6,8: preserve sections, comments and byte-identical unaffected rows.
    lines = []
    for line in text.splitlines(True):
        body, separator, comment = line.partition('#')
        if ' -> ' not in body:
            lines.append(line)
            continue
        left, right = body.strip().split(' -> ', 1)
        lefts = layer_alternatives(left, rows, base)
        rights = [item for token in right.split() for item in layer_alternatives(token, rows, base)]
        if lefts == [left] and rights == right.split():
            lines.append(line)
        else:
            for item in lefts:
                lines.append(item + ' -> ' + ' '.join(rights) + ('  #' + comment.rstrip('\n') if separator else '') + '\n')
    return ''.join(lines)


def rewrite_dynamic(text, rows, future, errors):
    # Rule 7: validate the resulting on-disk regular file for every dynamic key.
    def key(match):
        # Rule 7: map and validate each file/function key.
        path = map_path(match[1], rows)
        if path not in future:
            errors.append(f'dynamic key missing file: {path}:{match[2]}')
        return '"' + path + ':' + match[2] + '"'
    return re.sub(r'"([^"\n]+\.go):([A-Za-z_][A-Za-z_0-9]*)"', key, text)


def rewrite_replaces(text, replacements):
    # Rules 2,5: repair root replace even when the whole module is already moved.
    for source, destination in replacements.items():
        text = re.sub(r'(=>\s+\./)' + re.escape(source) + r'(?=\s|$)',
                      lambda match: match[1] + destination, text)
    return text


def excluded(path, map_file):
    # Rules 5,8: migration tools and their data must survive their own maps.
    return path in (map_file, 'scripts/split/move.sh', 'scripts/split/move_test.sh') or path.startswith('scripts/split/testdata/')


def plan_references(plan, tracked, text_files, rows, nested, module, map_file, errors):
    # Rules 1-8: compute every destination's contents from its canonical old source.
    roots = whole_roots(rows)
    base = roots.get('internal', 'internal')
    future = future_files(plan)
    twins = set(plan.removals.values())
    for source in tracked:
        destination = plan.moves.get(source, plan.removals.get(source, source))
        if source in twins or excluded(source, map_file) or not os.path.isfile(source):
            continue
        if module_owner(source, nested) or module_owner(destination, nested):
            continue
        try:
            original = pathlib.Path(source).read_text()
        except UnicodeError:
            continue
        text = original
        if source.endswith('.go'):
            text = rewrite_go_literals(text, source, destination, module, nested, rows, future)
            text = rewrite_embeds(text, source, destination, rows, errors)
        if source in text_files and source != 'scripts/layering/rules.txt':
            text = rewrite_text(text, source, destination, module, nested, rows, roots, future)
        if source == 'go.mod':
            text = rewrite_replaces(text, plan.replacements)
        if source == 'scripts/layering/rules.txt':
            text = rewrite_layering(text, rows, base)
        if source == 'scripts/inventory/dynamic.go':
            text = rewrite_dynamic(text, rows, future, errors)
        if text != original:
            plan.rewrites[destination] = text
            print('REWRITE ' + destination)
            print(''.join(difflib.unified_diff(original.splitlines(True), text.splitlines(True),
                                             fromfile=source, tofile=destination)), end='')
        if source.endswith('.go') and (text != original or source != destination):
            plan.gofiles.append(destination)


def validate_plan(errors):
    # Rules 3,4,5,7: all plan failures precede all writes.
    if errors:
        for error in errors:
            print('ERROR ' + error, file=sys.stderr)
        raise SystemExit(1)


def chunks(paths):
    # Rules 1,3: batch Git arguments below roughly 100 KB.
    chunk = []
    size = 0
    for path in paths:
        if chunk and size + len(os.fsencode(path)) + 1 > 90000:
            yield chunk
            chunk = []
            size = 0
        chunk.append(path)
        size += len(os.fsencode(path)) + 1
    if chunk:
        yield chunk


def execute_plan(plan, nested, others):
    # Rules 1,3,4,5,8: execute the validated plan, then format and regenerate.
    try:
        for source, destination in plan.units.items():
            if source == destination:
                continue
            pathlib.Path(destination).parent.mkdir(parents=True, exist_ok=True)
            subprocess.run(['git', 'mv', '--', source, destination], check=True)
            for path in others:
                if path.startswith(source + '/'):
                    pathlib.Path(path).parent.mkdir(parents=True, exist_ok=True)
                    os.rename(destination + path[len(source):], path)
        groups = {}
        for source, destination in plan.moves.items():
            if module_owner(source, nested):
                continue
            directory = P.dirname(destination) or '.'
            pathlib.Path(directory).mkdir(parents=True, exist_ok=True)
            if P.basename(source) == P.basename(destination):
                groups.setdefault(directory, []).append(source)
            else:
                subprocess.run(['git', 'mv', '--', source, destination], check=True)
        for directory, sources in groups.items():
            for chunk in chunks(sources):
                subprocess.run(['git', 'mv', '--', *chunk, directory + '/'], check=True)
        for chunk in chunks(plan.removals):
            subprocess.run(['git', 'rm', '-f', '--', *chunk], check=True)
        for path, text in plan.rewrites.items():
            pathlib.Path(path).write_text(text)
        parents = {P.dirname(path) for path in [*plan.moves, *plan.removals]}
        for parent in sorted(parents, key=len, reverse=True):
            while parent:
                try:
                    os.rmdir(parent)
                except OSError:
                    break
                parent = P.dirname(parent)
        for chunk in chunks(plan.gofiles):
            subprocess.run(['gofmt', '-w', *chunk], check=True)
        if pathlib.Path('Makefile').exists():
            if re.search(r'^inventory\s*:', pathlib.Path('Makefile').read_text(), re.M):
                subprocess.run(['make', 'inventory'], check=True)
    except Exception as error:
        raise SystemExit(f'Execution failed: {error}; working tree is changed') from error


def leftover_report(tracked, rows, nested, module, map_file, before=False):
    # Rule 8: informational path-aware report, excluding history and mapped destinations.
    print('Remaining old paths need a human look' + (' (before the move)' if before else '') + ':')
    prefixes = [re.escape(old.split('*', 1)[0]) for old, new in rows]
    candidate = re.compile('|'.join(prefixes)) if prefixes else re.compile(r'(?!)')
    for path in tracked:
        if excluded(path, map_file) or module_owner(path, nested):
            continue
        if path.startswith('docs/history/') or path in ('CHANGELOG.md', 'PARITY.md'):
            continue
        try:
            lines = pathlib.Path(path).read_text().splitlines()
        except (OSError, UnicodeError):
            continue
        for number, line in enumerate(lines, 1):
            if not candidate.search(line):
                continue
            normalized = ROOT_EXPRESSION.sub(lambda match: './' + match[2], line)
            normalized = REPORT_VARIABLE.sub(lambda match: './' + re.sub(r'^(?:\.\./)+', '', match[1]), normalized)
            found = False
            for match in TOKEN.finditer(normalized):
                token = match[0]
                core = token.removeprefix('./')
                if core.startswith(module + '/'):
                    core = core[len(module) + 1:]
                if any(core == new.rstrip('/') or core.startswith(new.rstrip('/') + '/') for old, new in rows if '*' not in new):
                    continue
                for old, new in rows:
                    if not core.startswith(old.split('*', 1)[0]):
                        continue
                    if '/' not in old and not core.startswith(old + '/'):
                        continue
                    if map_path(core, [(old, new)], symbol=True) != core:
                        found = True
            if found:
                print(f'{path}:{number}: {line}')


def main(argv):
    # Rules 1-8: read, plan, validate, print, then optionally execute.
    parser = argparse.ArgumentParser()
    parser.add_argument('--map', default='scripts/split/pathmap.txt')
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--text', action='append', default=[])
    args = parser.parse_args(argv)
    os.chdir(git(['rev-parse', '--show-toplevel']))
    map_file = P.relpath(os.path.abspath(args.map), os.getcwd())
    errors = []
    rows = parse_map(args.map, errors)
    tracked = git_files(['ls-files'])
    nested = discover_modules('.')
    module = re.search(r'^module\s+(\S+)', pathlib.Path('go.mod').read_text(), re.M)[1]
    plan = plan_operations(tracked, rows, nested, errors)
    for directory in nested:
        if directory in plan.units:
            print('MOVE MODULE ' + directory + ' -> ' + plan.units[directory])
        else:
            print('SKIP nested module ' + directory)
    for old, new in rows:
        if not glob.glob(old) and not glob.glob(new):
            print(f'NO-OP absent: {old} -> {new}')
    others = git_files(['ls-files', '--others', '--exclude-standard'])
    others += git_files(['ls-files', '--others', '--ignored', '--exclude-standard'])
    for path in others:
        if map_path(path, rows) != path:
            print('LEAVE untracked/ignored ' + path)
    text_files = set(git_files(['ls-files', '--', *TEXT_PATHS, *args.text]))
    protected_modules = dict(nested)
    for source, destination in plan.units.items():
        protected_modules[destination] = nested[source]
    plan_references(plan, tracked, text_files, rows, protected_modules, module, map_file, errors)
    for source, destination in plan.moves.items():
        print(f'MOVE {source} -> {destination}')
    for source in plan.removals:
        print('REMOVE identical ' + source)
    validate_plan(errors)
    if args.dry_run:
        leftover_report(tracked, rows, nested, module, map_file, before=True)
        print('Dry run: nothing changed')
        return
    sys.stdout.flush()
    execute_plan(plan, nested, others)
    print('Applied plan' if plan.moves or plan.removals or plan.rewrites else 'Nothing changed')
    try:
        leftover_report(git_files(['ls-files']), rows, discover_modules('.'), module, map_file)
    except Exception as error:
        raise SystemExit(f'Report failed: {error}; working tree is changed') from error


main(sys.argv[1:])
PY
