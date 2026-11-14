#!/usr/bin/env bash
# Gate 1 of the split and rules tracks (slice D0-S4): run EVERY step of
# `make check`, even after one fails, and pass only when the failures found
# equal scripts/split/known-red.txt exactly. `make check` stops at its first
# red step, so one known failure used to hide every later one.
#
#   bash scripts/split/check-all.sh                # gate 1, under the 2G memory cap
#   bash scripts/split/check-all.sh --golden --slice <slice id>  # gates 1+2
#   bash scripts/split/check-all.sh --browser --list # browser steps and baseline
#   --golden appends comparisons; --slice IDS limits the golden diff (optional).
#   bash scripts/split/check-all.sh --list         # step ids and commands, runs nothing
#   bash scripts/split/check-all.sh --only 'go vet' --only scripts/check-html.sh
#   bash scripts/split/check-all.sh --skip scripts/test-integration.sh --skip scripts/check-fresh-install.sh   # everything that needs no database
#   bash scripts/split/check-all.sh --quiet --out DIR
#   bash scripts/split/check-all.sh --steps-file FILE --known-red FILE   # another step list
#
# The step list comes from `make -n check` at run time. Exit 0: failures equal
# the known-red entries. Exit 1: a new failure, or a listed entry that passes
# now (delete it). Exit 2: usage, a malformed known-red line, no steps.
# A step may print `check-all-failure: <name>` at the start of a line.
# This adds <name> to its failure identities when the step fails.
# --no-cap (or CHECK_ALL_NO_CAP=1) disables the default cap; --cap is a no-op.
# Logs, results.tsv and summary.tsv land in --out (default: a new temp dir).
# See scripts/split/BASELINE.md for the measured times and the fast loop.
set -u
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P) || exit 2
cd "$repo" || exit 2
steps_file= out= known_red="$repo/scripts/split/known-red.txt"
quiet=0 list=0 cap=1 browser=0 golden=0 slice_set=0 red_set=0
slice=
[[ ${CHECK_ALL_NO_CAP:-0} == 1 ]] && cap=0
only=() skip=() args=()
error() { echo "check-all: $*" >&2; exit 2; }
while (($#)); do
    case $1 in
        --cap) shift ;;
        --no-cap) cap=0; shift ;;
        --browser) browser=1; args+=("$1"); shift ;;
        --golden) golden=1; args+=("$1"); shift ;;
        --quiet) quiet=1; args+=("$1"); shift ;;
        --list) list=1; args+=("$1"); shift ;;
        --steps-file|--known-red|--out|--only|--skip|--slice)
            (($# >= 2)) || error "missing value for $1"
            args+=("$1" "$2")
            case $1 in
                --steps-file) steps_file=$2 ;;
                --known-red) known_red=$2; red_set=1 ;;
                --slice) slice=$2; slice_set=1 ;;
                --out) out=$2 ;;
                --only) only+=("$2") ;;
                --skip) skip+=("$2") ;;
            esac
            shift 2 ;;
        *) error "unknown option: $1" ;;
    esac
done
(( ! ${#only[@]} || ! ${#skip[@]} )) || error '--skip cannot be combined with --only'
if ((browser)); then
    [[ -z $steps_file ]] && (( ! red_set && ! golden )) || error '--browser cannot be combined with --steps-file, --known-red or --golden'
    steps_file=$repo/scripts/split/browser-steps.txt
    known_red=$repo/scripts/split/browser-known-red.txt
fi
(( ! slice_set || golden )) || error '--slice requires --golden'
# CHECK_ALL_GOLDEN_DIR is a test-only override for a stub golden harness.
golden_dir=${CHECK_ALL_GOLDEN_DIR:-scripts/golden}
if ((golden)); then
    [[ -f $golden_dir/diff.mjs && -f $golden_dir/run.mjs ]] &&
        grep -q -- '--host' "$golden_dir/run.mjs" ||
        error '--golden needs the D0-S3a/D0-S3b golden harness (scripts/golden/run.mjs --host ...), which is not in this tree'
    ref=${GOLDEN_REF_BIN:-/usr/local/bin/simple-host}
    [[ -f $ref && -x $ref ]] || error "GOLDEN_REF_BIN is not an executable file: $ref"
fi
if [[ ! ${FRESH_DB+x} ]]; then
    hash=$(printf '%s' "$repo" | sha1sum)
    export FRESH_DB="sh_freshcheck_${hash:0:8}"
fi
if ((cap && ! list)); then
    cap_error='the memory cap needs passwordless sudo and systemd-run; run with --no-cap to go without it (not a gate result)'
    command -v systemd-run >/dev/null && sudo -n true || error "$cap_error"
    env_args=() preserve=()
    for key in FRESH_DB TEST_COUNT TEST_PKGS TEST_WORKERS TMPDIR TEST_PG_URL PSQL PGPORT PGPASSWORD GOLDEN_REF_BIN GOLDEN_REF_HACK_BIN GOLDEN_HACK_BIN GOLDEN_PG_URL CHECK_ALL_GOLDEN_DIR BASE OWNER_KEY_FILE OTHER_KEY_FILE PLAYWRIGHT PLAYWRIGHT_DIR CHROMIUM NAVIGATION_SHOTS; do
        if [[ -v $key ]]; then export "$key"; env_args+=(-E "$key"); preserve+=("$key"); fi
    done
    preserve_list=$(IFS=,; echo "${preserve[*]}")
    exec sudo --preserve-env="$preserve_list" systemd-run --expand-environment=no --wait --pipe --collect -p MemoryMax=2G \
        -p "WorkingDirectory=$repo" --uid="$(id -u)" --gid="$(id -g)" \
        -E CHECK_ALL_CAPPED=1 -E "HOME=$HOME" -E "PATH=$PATH" -E "GOCACHE=$(go env GOCACHE)" \
        -E "GOMODCACHE=$(go env GOMODCACHE)" "${env_args[@]}" \
        bash scripts/split/check-all.sh --no-cap "${args[@]}"
fi
# Python handles text formats; Bash owns shell execution and pipeline statuses.
parse() {
python3 - "$@" <<'PY'
import sys, re, pathlib, shlex, os
mode, *args = sys.argv[1:]
# Read diagnostics as text, including invalid bytes from failed commands.
def read(path):
    with open(path, errors='replace', newline='') as f:
        return f.read()
# Encode each name before adding structural separators; details remain free text.
def component(s):
    s = s.replace('%', '%25')
    for char, code in [('|', '%7C'), ('>', '%3E'), ('\t', '%09'), ('\n', '%0A'), ('\r', '%0D')]:
        s = s.replace(char, code)
    s = re.sub(r'^ +| +$', lambda m: '%20' * len(m[0]), s)
    return s

# Keep diagnostics pasteable in the baseline format.
def clean(s):
    return s.strip().replace('|', '/').replace('\t', ' ')
# Reject malformed or duplicate baseline entries before running steps.
def known(path):
    entries = {}
    for n, line in enumerate(read(path).splitlines(), 1):
        if not line.strip() or line.startswith('#'): continue
        fields = line.split(' | ', 3)
        if len(fields) != 4 or any(not x.strip() for x in fields):
            raise ValueError(f'{path}:{n}: malformed known-red line: {line}')
        pair = tuple(fields[:2])
        if pair in entries:
            raise ValueError(f'{path}:{n}: duplicate known-red pair: {line}')
        entries[pair] = line
    return entries
# Allocate final ids against all ids already used, including numbered ones.
def unique_steps(steps):
    used, result = set(), []
    for base, command in steps:
        ident, number = base, 1
        while ident in used:
            number += 1
            ident = f'{base}#{number}'
        used.add(ident)
        result.append((ident, command))
    return result

# Read the module prefix from the checkout rather than embedding an import path.
def package(pkg):
    module = re.search(r'^module\s+(\S+)', read('go.mod'), re.M)[1]
    return pkg.removeprefix(module + '/')

# Recover Go identities without counting raw output inside summariser blocks.
def go_failures(lines):
    headers = re.compile(r'^--- FAIL: (\S+) \(([^)]+)\)(?: \[iteration \d+\])? ---$')
    matches = [headers.match(line) for line in lines]
    if any(matches):
        return [(i, component(package(m[2])) + ':' + component(m[1])) for i, m in enumerate(matches) if m]
    result, pending = [], []
    module = re.search(r'^module\s+(\S+)', read('go.mod'), re.M)[1]
    for i, line in enumerate(lines):
        if m := re.search(r'--- FAIL: (\S+) \([\d.]+s\)', line):
            pending.append((i, component(m[1])))
        elif m := re.search(r'--- FAIL: ((?:Test|Example|Benchmark|Fuzz)\S*)\s*$', line):
            pending.append((i, component(m[1])))
        if m := re.match(r'^\s*(FAIL|ok)\s+(\S+)(?:\s|$)', line):
            if '/' in m[2] or m[2] == module:
                prefix = component(package(m[2])) + ':' if m[1] == 'FAIL' else ''
                result.extend((i, prefix + name) for i, name in pending)
                pending = []
    return result + pending

# Track TAP suite announcements and results by their indentation.
def tap_failures(lines):
    path, result = {}, []
    for i, line in enumerate(lines):
        if m := re.match(r'^( *)(?:# Subtest: (.*)|((?:not )?ok) \d+(?: -)? (.*))$', line):
            level = len(m[1]) // 4
            for key in list(path):
                if key >= level: del path[key]
            title = m[2] if m[2] is not None else m[4]
            # A backslash escapes only a hash or another backslash in titles.
            directive = None
            decoded, pos = '', 0
            while pos < len(title):
                char = title[pos]
                if char == '\\' and pos + 1 < len(title) and title[pos + 1] in '#\\':
                    decoded += title[pos + 1]
                    pos += 2
                    continue
                if char == '#':
                    directive = title[pos + 1:]
                    decoded = decoded.rstrip()
                    break
                decoded += char
                pos += 1
            path[level] = component(decoded)
            if m[3] == 'not ok' and not (directive is not None and re.match(r'\s*(TODO|SKIP)\b', directive, re.I)):
                detail = ''
                for following in lines[i+1:]:
                    if following.strip() and len(following) - len(following.lstrip()) <= len(m[1]): break
                    if e := re.match(r'\s+error:\s*(.*)', following):
                        detail = e[1] if e[1] not in ('|', '>') else ''
                        break
                result.append((' > '.join(path[k] for k in sorted(path)), detail or line))
    return result

# Combine named failures, preserving the first diagnostic for each identity.
def failures(lines, status):
    names = {}
    def add(name, detail):
        names.setdefault(name, clean(detail))
    for i, name in go_failures(lines):
        add(name, next((s for s in lines[i+1:] if s.strip()), lines[i]))
    for name, detail in tap_failures(lines): add(name, detail)
    skips = False
    for line in lines:
        if line.startswith('== DB-RELATED SKIPS ('): skips = True; continue
        if skips and not line.strip(): skips = False
        name, detail = None, line
        if m := re.match(r'^check-all-failure: (.+)', line): name = component(m[1])
        elif m := re.search(r'--- build failed: (\S+) ---', line): name = 'build:' + component(package(m[1]))
        elif m := re.match(r'\s*FAIL\s+(\S+)\s+\[build failed\]', line): name = 'build:' + component(package(m[1]))
        elif m := re.search(r'--- package output: (\S+) ---', line): name = 'package:' + component(package(m[1]))
        elif skips and (m := re.match(r'\s+\[package\]\s+(\S+):\s*(.*)', line)):
            name, detail = 'dbskip:package:' + component(package(m[1])), m[2]
        elif skips and (m := re.match(r'\s+(\S+)\s+\(([^)]+)\):\s*(.*)', line)):
            name, detail = 'dbskip:' + component(package(m[2])) + ':' + component(m[1]), m[3]
        if name is not None: add(name, detail)
    for line in lines:
        if (m := re.fullmatch(r'go test itself exited (\d+)', line)) and m[1] != '1':
            add('go-test-exit:' + m[1], line)
    if int(status) in (124, 125) or int(status) >= 126: add('exit:' + status, 'step exited ' + status)
    if not names:
        candidates = [s for s in lines if re.search(r'fail|error|missing|not ok', s, re.I)]
        add('*', next(iter(candidates or [s for s in lines if s.strip()]), ''))
    return names

# Verify every planned step was recorded once, in its original order.
def verify_results(out, plan):
    planned = pathlib.Path(plan).read_bytes().split(b'\0')[:-1]
    ids = [s.decode() for s in planned[::3]]
    path = pathlib.Path(out + '/results.tsv')
    rows = read(path).splitlines() if path.is_file() else []
    if [r.split('\t')[0] for r in rows] != ids or any(len(r.split('\t')) != 4 for r in rows):
        raise ValueError(f'results are incomplete ({len(rows)} of {len(ids)} steps recorded)')

try:
    if mode == 'prepare':
        source, is_file, dest, red, listing, golden, out, golden_dir, slice_set, slice_ids, skip_count, *selected = args
        commands, pending = [], ''
        for line in read(source).splitlines():
            if not pending and (not line.strip() or (is_file == '1' and line.lstrip().startswith('#')) or
                                (is_file == '0' and re.match(r'^make(?:\[\d+\])?:', line))):
                continue
            pending += line
            if pending.endswith('\\'):
                pending = pending[:-1] + ' '
                continue
            commands.append(pending.strip()); pending = ''
        if pending.strip(): commands.append(pending.strip())
        if not commands: raise ValueError('no steps found')
        steps = []
        for command in commands:
            explicit = None
            if is_file == '1' and ' := ' in command:
                prefix, body = command.split(' := ', 1)
                if prefix and '\t' not in prefix and ' | ' not in prefix:
                    explicit, command = prefix, body
            ident = ' '.join(command.split())
            if explicit is not None: ident = explicit
            elif 'gofmt -l' in command: ident = 'gofmt'
            elif m := re.search(r'\bgo\s+([\w-]+)\b', command): ident = 'go ' + m[1]
            elif re.search(r'\bnode\s+--test\b', command): ident = 'node --test'
            elif m := re.search(r'\b(?:bash|sh|python3|node)\s+([^-\s][^\s]*)', command): ident = m[1].strip('\'"')
            elif m := re.search(r'(?:\bmake|\$\(MAKE\))\s+([^\s]+)', command): ident = 'make ' + m[1]
            steps.append((ident, command))
        if golden == '1':
            root = out + '/golden'
            candidate = root + '/candidate.bin'
            def shell(words):
                return shlex.join(words)
            steps.append(('golden:build', 'CGO_ENABLED=0 ' + shell(['go', 'build', '-o', candidate, './cmd/server'])))
            for side, host, hack, target in [
                ('reference', os.environ.get('GOLDEN_REF_BIN') or '/usr/local/bin/simple-host', 'GOLDEN_REF_HACK_BIN', 'ref'),
                ('candidate', candidate, 'GOLDEN_HACK_BIN', 'cand'),
            ]:
                words = ['node', golden_dir + '/run.mjs', '--host', host]
                if os.environ.get(hack): words += ['--hack', os.environ[hack]]
                words += ['--out', root + '/' + target]
                steps.append(('golden:' + side, shell(words)))
            words = ['node', golden_dir + '/diff.mjs', root + '/ref', root + '/cand']
            if slice_set == '1': words += ['--slice', slice_ids]
            steps.append(('golden:diff', shell(words)))
        steps = unique_steps(steps)
        unknown = set(selected) - {s[0] for s in steps}
        if unknown: raise ValueError('unknown step id: ' + ', '.join(sorted(unknown)))
        if listing == '1':
            for ident, command in steps: print(ident + '\t' + command)
        else:
            known(red)  # Reject bad baselines before executing any commands.
            if selected:
                steps = [(ident, command) for ident, command in steps
                         if (ident not in selected if int(skip_count) else ident in selected)]
            if not steps: raise ValueError('no steps selected')
            with open(dest, 'wb') as f:
                for index, (ident, command) in enumerate(steps, 1):
                    safe = re.sub(r'[^A-Za-z0-9_.-]+', '_', ident)[:180]
                    for field in (ident, command, f'{index:02d}-{safe}.log'):
                        f.write(field.encode() + b'\0')
    elif mode == 'verify':
        verify_results(*args)
    elif mode == 'report':
        out, red, total, partial, no_cap = args
        suffix = ' [no memory cap]' if no_cap == '1' else ''
        expected = known(red)
        rows, found = [], {}
        for raw in read(out + '/results.tsv').splitlines():
            ident, status, seconds, log = raw.split('\t')
            names = {}
            lines = read(out + '/' + log).split('\n')
            if status != '0':
                names = failures(lines, status)
                for name, detail in names.items(): found[ident, name] = f'{ident} | {name} | ? | {detail}'
            rows.append((ident, 'ok' if status == '0' else 'FAIL', status, seconds, ','.join(names)))
        if partial == '1':
            ran = {r[0] for r in rows}
            expected = {k:v for k,v in expected.items() if k[0] in ran}
            print('Partial run (--only/--skip): steps that did not run are left out of the known-red comparison.')
        with open(out + '/summary.tsv', 'w') as f:
            for row in rows: f.write('\t'.join(row) + '\n')
        print('\nResult\tSeconds\tStep')
        for ident, result, status, seconds, names in rows: print(f'{result}\t{seconds}\t{ident}')
        print(f'Total wall-clock seconds: {total}')
        unexpected, stale = found.keys() - expected.keys(), expected.keys() - found.keys()
        for title, entries in [('Failures found', found), ('Unexpected failures (not in known-red.txt)', {k:found[k] for k in sorted(unexpected)}), ('Stale known-red entries (listed but passing now)', {k:expected[k] for k in sorted(stale)})]:
            print(f'{title} ({len(entries)}):')
            for value in entries.values(): print(value)
        print('Logs: ' + out)
        if unexpected or stale:
            print(f'check-all: FAIL — {len(unexpected)} unexpected, {len(stale)} stale' + suffix)
            sys.exit(1)
        print(f'check-all: PASS — failures equal known-red.txt ({len(expected)} known)' + suffix)
except (OSError, ValueError) as e:
    print('check-all: ' + str(e), file=sys.stderr)
    sys.exit(2)
PY
}
work=$(mktemp -d "${TMPDIR:-/tmp}/check-all-work.XXXXXXXX") || exit 2
trap 'rm -rf -- "$work"' EXIT
if [[ -n $steps_file ]]; then
    source=$steps_file; is_file=1
else
    source=$work/steps; is_file=0
    make -n check >"$source" 2>"$work/make.err" || { cat "$work/make.err" >&2; error 'make -n check failed'; }
fi
if ((golden)); then
    if ((list)); then
        [[ -n $out ]] || out='<out>'
    else
        if [[ -z $out ]]; then out=$(mktemp -d "${TMPDIR:-/tmp}/check-all.XXXXXXXX") || exit 2; fi
        mkdir -p -- "$out/golden" || exit 2
    fi
fi
parse prepare "$source" "$is_file" "$work/plan" "$known_red" "$list" "$golden" "$out" "$golden_dir" "$slice_set" "$slice" "${#skip[@]}" "${only[@]}" "${skip[@]}" || exit 2
((list)) && exit 0
if [[ -z $out ]]; then out=$(mktemp -d "${TMPDIR:-/tmp}/check-all.XXXXXXXX") || exit 2; fi
mkdir -p -- "$out" || exit 2
: >"$out/results.tsv" || exit 2
io_failed=0
start=$SECONDS
while IFS= read -r -d '' ident && IFS= read -r -d '' command && IFS= read -r -d '' log; do
    step_start=$SECONDS
    if ((quiet)); then
        sh -c "$command" </dev/null 2>&1 | tee "$out/$log" >/dev/null
        pipeline=("${PIPESTATUS[@]}")
    else
        sh -c "$command" </dev/null 2>&1 | tee "$out/$log"
        pipeline=("${PIPESTATUS[@]}")
    fi
    status=${pipeline[0]}
    ((pipeline[1] == 0)) || io_failed=1
    seconds=$((SECONDS-step_start))
    result=ok; ((status != 0)) && result=FAIL
    printf '%s\t%ss\t%s\n' "$result" "$seconds" "$ident"
    printf '%s\t%s\t%s\t%s\n' "$ident" "$status" "$seconds" "$log" >>"$out/results.tsv" || io_failed=1
done <"$work/plan"
parse verify "$out" "$work/plan" || exit 2
((io_failed == 0)) || error "failed to write step logs or results.tsv"
partial=0; ((${#only[@]} || ${#skip[@]})) && partial=1
parse report "$out" "$known_red" "$((SECONDS-start))" "$partial" "$(( ${CHECK_ALL_CAPPED:-0} != 1 ))"
