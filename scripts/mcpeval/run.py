#!/usr/bin/env python3
"""Eval Simple Hack's MCP server with real models: each scenario in hack.yaml is
typed, as a person would, into the `claude` CLI (headless, only the local
fixture's server loaded), then checked against the server afterwards.

  python3 scripts/mcpeval/run.py hack [--models small,mid] [--only id,id]
         [--jobs 4] [--max-turns 15] [--repeat 1] [--save-baseline] [--size-only]

Every scenario gets a fresh fixture (scripts/mcpjam/fixture.sh): a copy of one seeded server.
Postgres: EVAL_PG_URL (a bare server URL, no path), else a throwaway Docker
container. Results land in scripts/mcpeval/out/ (gitignored; EVAL_OUT moves it); the summary is
compared with baseline-hack.json, which --save-baseline rewrites. (Simple Host,
the rewritten service in next/, is checked by scripts/mcpjam/host_checks.py.)"""
import argparse, concurrent.futures as cf, json, os, re, secrets, shutil, signal, socket, subprocess, sys, time, urllib.request, uuid
import yaml

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
OUT = os.environ.get("EVAL_OUT") or os.path.join(HERE, "out")
SERVER = {"hack": "simplehack"}
# As on simple-host.app: every site at its own address, and a visitor sign-in
# provider so named viewers work (nobody signs in, so Google is never called).
FIXTURE_ENV = {"PERSON_HOSTS": "serve", "SITE_HOSTS": "serve", "GOOGLE_OAUTH_CLIENT_ID": "mcpeval", "GOOGLE_OAUTH_CLIENT_SECRET": "mcpeval"}
# The models, by size: the claude CLI's aliases (EVAL_SMALL and EVAL_MID change them).
MODELS = {"small": os.environ.get("EVAL_SMALL", "haiku"), "mid": os.environ.get("EVAL_MID", "sonnet")}
SYSTEM = "You are a helpful assistant in a chat app. The person is not technical. Use the connected app's tools when they help."


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def sh(cmd, **kw):
    return subprocess.run(cmd, check=True, capture_output=True, text=True, **kw).stdout


class Postgres:
    def __init__(self):
        self.container = None
        self.url = os.environ.get("EVAL_PG_URL", "").rstrip("/")
        if not self.url:
            port, pw = free_port(), secrets.token_hex(8)
            self.container = sh(["docker", "run", "-d", "--rm", "-e", f"POSTGRES_PASSWORD={pw}", "-p", f"127.0.0.1:{port}:5432", "postgres:16-alpine", "-c", "max_connections=1000"]).strip()
            self.url = f"postgres://postgres:{pw}@127.0.0.1:{port}"
            for _ in range(60):
                if subprocess.run(["psql", self.url + "/postgres", "-XAtc", "select 1"], capture_output=True).returncode == 0:
                    break
                time.sleep(1)

    def create(self, name, template=None):
        sh(["psql", self.url + "/postgres", "-XAtc", f'CREATE DATABASE "{name}"' + (f' TEMPLATE "{template}"' if template else "")])
        return f"{self.url}/{name}?sslmode=disable"

    def drop(self, name):
        subprocess.run(["psql", self.url + "/postgres", "-XAtc", f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)'], capture_output=True)

    def close(self):
        if self.container:
            subprocess.run(["docker", "rm", "-f", "-v", self.container], capture_output=True)



class Fixture:
    """One running server with its seeded accounts (role -> key file). The
    first, the golden one, is seeded and stopped; every other starts from a
    copy of its database and directory, so none seeds again."""

    def __init__(self, product, binary, pg, work, golden=None):
        self.product, self.dir, self.pg = product, os.path.join(work, "fix"), pg
        self.db = "mcpeval_" + secrets.token_hex(6)
        self.port = free_port()
        if golden:
            shutil.copytree(golden.dir, self.dir, symlinks=True, ignore=shutil.ignore_patterns("*.pid", "*.log"))
        env = dict(os.environ, **FIXTURE_ENV, PRODUCT=product, BIN=binary, FIX=self.dir, PORT=str(self.port), TLS_PORT=str(free_port()),
                   DB_DSN=pg.create(self.db, golden and golden.db))
        sh(["bash", os.path.join(ROOT, "scripts/mcpjam/fixture.sh")], env=env)
        self.url = f"http://127.0.0.1:{self.port}/mcp"

    def key(self, role):
        return open(os.path.join(self.dir, ("user" if role in ("user", "organiser") else role) + ".key")).read().strip()

    def vars(self):
        out = {}
        for name in ("join.code", "event.slug"):
            p = os.path.join(self.dir, name)
            if os.path.exists(p):
                out[name.replace(".", "_")] = open(p).read().strip()
        return out

    def rpc(self, role, method, params):
        body = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method, "params": params}).encode()
        req = urllib.request.Request(self.url, data=body, headers={"Content-Type": "application/json", "Accept": "application/json, text/event-stream",
                                                                  "X-API-Key": self.key(role), "MCP-Protocol-Version": "2025-06-18"})
        return json.loads(urllib.request.urlopen(req, timeout=60).read())

    def call(self, role, tool, args):
        r = self.rpc(role, "tools/call", {"name": tool, "arguments": args}).get("result") or {}
        text = json.dumps(r.get("structuredContent")) if r.get("structuredContent") is not None else " ".join(c.get("text", "") for c in r.get("content", []))
        return bool(r.get("isError")), text

    def live(self, where):
        """The live copy of a published file, as the web server serves it: <owner>/<site>[/<path>]."""
        owner, site, path = (where.split("/", 2) + ["index.html"])[:3]
        try:
            return False, open(os.path.join(self.dir, "data/handles", owner, site, "current", path or "index.html"), errors="replace").read()
        except OSError as e:
            return True, str(e)

    def stop(self):
        for f in ("server.pid", "proxy.pid"):
            try:
                pid = int(open(os.path.join(self.dir, f)).read())
                os.kill(pid, 15)
                for _ in range(100):  # the golden database is copied only once no server holds it
                    os.kill(pid, 0)
                    time.sleep(0.1)
            except (OSError, ValueError):
                pass

    def close(self):
        self.stop()
        self.pg.drop(self.db)


def fill(value, v):
    """Replace {join_code}-style placeholders in strings, lists and maps."""
    if isinstance(value, str):
        return re.sub(r"\{(\w+)\}", lambda m: v.get(m.group(1), m.group(0)), value)
    if isinstance(value, list):
        return [fill(x, v) for x in value]
    if isinstance(value, dict):
        return {k: fill(x, v) for k, x in value.items()}
    return value


def run_claude(fx, role, model, prompts, max_turns, work):
    """Type each prompt in turn into one conversation; return the parsed trace."""
    cfg = os.path.join(work, "mcp.json")
    with open(cfg, "w") as f:
        json.dump({"mcpServers": {SERVER[fx.product]: {"type": "http", "url": fx.url, "headers": {"X-API-Key": fx.key(role)}}}}, f)
    cwd = os.path.join(work, "cwd")
    os.makedirs(cwd, exist_ok=True)
    session, trace = str(uuid.uuid4()), {"calls": [], "replies": [], "turns": 0, "cost_usd": 0.0, "errors": []}
    pending = {}
    for i, prompt in enumerate(prompts):
        cmd = ["claude", "-p", prompt, "--model", MODELS.get(model, model), "--max-turns", str(max_turns), "--output-format", "stream-json", "--verbose",
               "--setting-sources", "", "--strict-mcp-config", "--mcp-config", cfg, "--tools", "", "--system-prompt", SYSTEM,
               "--allowedTools", "mcp__" + SERVER[fx.product], "--permission-mode", "bypassPermissions"]
        cmd += ["--session-id", session] if i == 0 else ["--resume", session]
        p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, stdin=subprocess.DEVNULL, timeout=900)
        with open(os.path.join(work, f"trace-{i}.jsonl"), "w") as f:
            f.write(p.stdout)
        for line in p.stdout.splitlines():
            try:
                d = json.loads(line)
            except ValueError:
                continue
            if d.get("type") == "assistant":
                for c in d["message"]["content"]:
                    if c.get("type") == "tool_use":
                        name, args = c["name"].split("__")[-1], c.get("input") or {}
                        call = {"tool": name, "args": args}
                        if name == "run_tool":
                            call = {"tool": str(args.get("name")), "args": args.get("arguments") or {}, "via": "run_tool"}
                        pending[c["id"]] = call
                        trace["calls"].append(call)
                    elif c.get("type") == "text" and c["text"].strip():
                        trace["replies"].append(c["text"])
            elif d.get("type") == "user":
                for c in d["message"]["content"] if isinstance(d["message"]["content"], list) else []:
                    if isinstance(c, dict) and c.get("type") == "tool_result" and c.get("is_error"):
                        content = c.get("content")
                        text = content if isinstance(content, str) else " ".join(x.get("text", "") for x in content or [] if isinstance(x, dict))
                        call = pending.get(c.get("tool_use_id"), {})
                        call["error"] = text[:300]
            elif d.get("type") == "result":
                trace["turns"] += d.get("num_turns") or 0
                trace["cost_usd"] += d.get("total_cost_usd") or 0
                if d.get("subtype") != "success":
                    trace["errors"].append(d.get("subtype"))
        if p.returncode and not p.stdout:
            trace["errors"].append(p.stderr[-300:])
    # The CLI keeps each conversation under ~/.claude/projects/<cwd with dashes>.
    shutil.rmtree(os.path.join(os.path.expanduser("~/.claude/projects"), re.sub(r"[^A-Za-z0-9]", "-", cwd)), ignore_errors=True)
    return trace


UNKNOWN = re.compile(r"unknown_tool|No such tool|not available|unknown tool", re.I)


def check(fx, sc, trace, v):
    """Return the list of failed checks (empty means pass)."""
    failed = []
    names = [c["tool"] for c in trace["calls"]]
    for t in sc.get("must_call", []):
        if not any(n == t for n in names):
            failed.append(f"did not call {t}")
    for t in sc.get("must_not_call", []):
        if t in names:
            failed.append(f"called {t}")
    if "max_calls" in sc and len(names) > sc["max_calls"]:
        failed.append(f"{len(names)} calls > {sc['max_calls']}")
    reply = "\n".join(trace["replies"])
    if sc.get("says") and not re.search(sc["says"], reply, re.I | re.S):
        failed.append(f"reply lacks /{sc['says']}/")
    for c in fill(sc.get("checks", []), v):
        if "tool" in c:
            err, text = fx.call(c.get("as", "user"), c["tool"], c.get("args", {}))
            what = c["tool"]
        else:
            err, text = fx.live(c["live"])
            what = "live " + c["live"]
        if err != c.get("error", False):
            failed.append(f"{what}: {'error' if err else 'no error'} ({text[:120]})")
        elif c.get("match") and not re.search(c["match"], text, re.I | re.S):
            failed.append(f"{what} lacks /{c['match']}/")
        elif c.get("no_match") and re.search(c["no_match"], text, re.I | re.S):
            failed.append(f"{what} has /{c['no_match']}/")
    return failed


def run_one(product, binary, pg, golden, sc, model, args, n=0):
    work = os.path.join(OUT, "work", product, f"{sc['id']}-{model}" + (f"-{n}" if n else ""))
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)
    fx, started = None, time.time()
    try:
        fx = Fixture(product, binary, pg, work, golden)
        v = fx.vars()
        for step in fill(sc.get("setup", []), v):
            err, text = fx.call(step.get("as", "user"), step["tool"], step.get("args", {}))
            if err:
                raise RuntimeError(f"setup {step['tool']}: {text[:200]}")
        prompts = fill(sc["prompt"] if isinstance(sc["prompt"], list) else [sc["prompt"]], v)
        trace = run_claude(fx, sc.get("as", "user"), model, prompts, args.max_turns, work)
        failed = check(fx, sc, trace, v)
    except Exception as e:  # a broken fixture is a harness failure, reported as such
        trace, failed = {"calls": [], "replies": [], "turns": 0, "cost_usd": 0, "errors": [str(e)]}, [f"harness: {e}"]
    finally:
        if fx:
            fx.close()
    errs = [c for c in trace["calls"] if c.get("error")]
    return {"id": sc["id"], "model": model, "pass": not failed, "failed": failed, "turns": trace["turns"], "cost_usd": round(trace["cost_usd"], 4),
            "calls": [c["tool"] + ("!" if c.get("error") else "") for c in trace["calls"]],
            "unknown_tools": [c["tool"] for c in errs if UNKNOWN.search(c["error"])],
            "arg_errors": [f"{c['tool']}: {c['error'][:160]}" for c in errs if not UNKNOWN.search(c["error"])],
            "reply": "\n".join(trace["replies"])[-600:], "seconds": round(time.time() - started), "harness_errors": trace["errors"]}


def listing_cost(product, binary, pg, golden, model, with_tokens=True):
    """Size of what the server costs every conversation: tools/list plus instructions."""
    work = os.path.join(OUT, "work", product, "listing")
    shutil.rmtree(work, ignore_errors=True)
    os.makedirs(work)
    fx = Fixture(product, binary, pg, work, golden)
    try:
        init = fx.rpc("user", "initialize", {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "claude-code", "version": "1"}})["result"]
        tools = fx.rpc("user", "tools/list", {})["result"]["tools"]
        seen = sum(len(json.dumps({k: t.get(k) for k in ("name", "title", "description", "inputSchema")})) for t in tools)
        size = {"tools": len(tools), "instruction_chars": len(init.get("instructions", "")), "model_facing_bytes": seen + len(init.get("instructions", "")),
                "tools_list_bytes": len(json.dumps(tools))}

        if not with_tokens:
            return size

        def tokens(with_server):
            cfg = os.path.join(work, "mcp.json")
            json.dump({"mcpServers": {SERVER[product]: {"type": "http", "url": fx.url, "headers": {"X-API-Key": fx.key("user")}}} if with_server else {}}, open(cfg, "w"))
            out = sh(["claude", "-p", "Reply OK", "--model", MODELS.get(model, model), "--max-turns", "1", "--output-format", "json", "--setting-sources", "", "--strict-mcp-config",
                      "--mcp-config", cfg, "--tools", "", "--system-prompt", SYSTEM, "--no-session-persistence"], cwd=work, stdin=subprocess.DEVNULL)
            u = json.loads(out)["usage"]
            return u["input_tokens"] + u["cache_creation_input_tokens"] + u["cache_read_input_tokens"]

        size["tokens"] = tokens(True) - tokens(False)
        return size
    finally:
        fx.close()


def report(product, results, size, models, base):
    rate = {m: round(100 * sum(r["pass"] for r in results if r["model"] == m) / max(1, sum(r["model"] == m for r in results))) for m in models}
    lines = [f"# {product}: {', '.join(f'{m} {rate[m]}%' for m in models)} pass; tools/list {size['tools']} tools, ~{size['tokens']} tokens", "",
             "| scenario | " + " | ".join(models) + " |", "|---|" + "---|" * len(models)]
    by = {}
    for r in results:
        by.setdefault((r["id"], r["model"]), []).append(r)
    for sid in dict.fromkeys(r["id"] for r in results):
        cells = []
        for m in models:
            runs = by.get((sid, m), [])
            if not runs:
                cells.append("")
                continue
            ok = sum(r["pass"] for r in runs)
            note = ("PASS" if ok == len(runs) else "FAIL") + (f" {ok}/{len(runs)}" if len(runs) > 1 else "")
            note += f" {sum(len(r['calls']) for r in runs) / len(runs):g} calls {sum(r['turns'] for r in runs) / len(runs):g}t"
            errs = sum(len(r["unknown_tools"]) + len(r["arg_errors"]) for r in runs)
            if errs:
                note += f" {errs} err"
            failed = next((r["failed"] for r in runs if not r["pass"]), None)
            if failed:
                note += ": " + re.sub(r"\s+", " ", "; ".join(failed))[:140]
            cells.append(note.replace("|", "/"))
        lines.append(f"| {sid} | " + " | ".join(cells) + " |")
    if base:
        lines += ["", "vs baseline: " + ", ".join(f"{m} {base.get('pass_rate', {}).get(m, '?')}% -> {rate[m]}%" for m in models) +
                  f"; tokens {base.get('tokens', '?')} -> {size['tokens']}; tools {base.get('tools', '?')} -> {size['tools']}"
                  f"; bytes {base.get('model_facing_bytes', '?')} -> {size['model_facing_bytes']}"]
    return "\n".join(lines), rate


def load_baseline(product):
    path = os.path.join(HERE, f"baseline-{product}.json")
    return json.load(open(path)) if os.path.exists(path) else None


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("product", choices=["hack"])
    ap.add_argument("--models", default="small,mid")
    ap.add_argument("--only", default="")
    ap.add_argument("--jobs", type=int, default=4)
    ap.add_argument("--max-turns", type=int, default=15)
    ap.add_argument("--repeat", type=int, default=1, help="runs per scenario and model")
    ap.add_argument("--save-baseline", action="store_true")
    ap.add_argument("--size-only", action="store_true", help="measure the tool listing, run no model")
    args = ap.parse_args()
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))  # run the cleanup in finally blocks
    models = args.models.split(",")
    scenarios = yaml.safe_load(open(os.path.join(HERE, args.product + ".yaml")))
    if args.only:
        scenarios = [s for s in scenarios if s["id"] in args.only.split(",")]
    os.makedirs(OUT, exist_ok=True)
    binary = os.environ.get("BIN") or os.path.join(OUT, "simple-" + args.product)
    if not os.environ.get("BIN"):
        sh(["go", "build", "-o", binary, "./cmd/simple-" + args.product], cwd=ROOT, env=dict(os.environ, CGO_ENABLED="0"))
    pg, golden = Postgres(), None
    pool = cf.ThreadPoolExecutor(args.jobs)
    try:
        golden_dir = os.path.join(OUT, "work", args.product, "golden")
        shutil.rmtree(golden_dir, ignore_errors=True)
        golden = Fixture(args.product, binary, pg, golden_dir)
        golden.stop()
        size = listing_cost(args.product, binary, pg, golden, models[0], not args.size_only)
        if args.size_only:
            base = load_baseline(args.product)
            print(f"# {args.product}: tools/list {size['tools']} tools, {size['model_facing_bytes']} model-facing bytes")
            if base:
                print(f"vs baseline: tools {base.get('tools')} -> {size['tools']}; bytes {base.get('model_facing_bytes')} -> {size['model_facing_bytes']}")
            return
        futures = [pool.submit(run_one, args.product, binary, pg, golden, sc, m, args, n) for n in range(args.repeat) for sc in scenarios for m in models]
        results = []
        for f in cf.as_completed(futures):
            r = f.result()
            results.append(r)
            print(f"{'PASS' if r['pass'] else 'FAIL'} {r['id']} [{r['model']}] {' '.join(r['calls'])} {re.sub(r'\s+', ' ', '; '.join(r['failed']))[:300]}", flush=True)
    finally:
        pool.shutdown(cancel_futures=True)
        if golden:
            pg.drop(golden.db)
        pg.close()
    order = {s["id"]: i for i, s in enumerate(scenarios)}
    results.sort(key=lambda r: (order[r["id"]], models.index(r["model"])))
    base = load_baseline(args.product)
    text, rate = report(args.product, results, size, models, base)
    stamp = time.strftime("%Y%m%d-%H%M%S")
    json.dump({"product": args.product, "size": size, "pass_rate": rate, "results": results}, open(os.path.join(OUT, f"{args.product}-{stamp}.json"), "w"), indent=1)
    open(os.path.join(OUT, f"{args.product}-{stamp}.md"), "w").write(text + "\n")
    print(text)
    if args.save_baseline and not args.only:
        with open(os.path.join(HERE, f"baseline-{args.product}.json"), "w") as f:
            json.dump({"pass_rate": rate, "tokens": size["tokens"], "tools": size["tools"], "model_facing_bytes": size["model_facing_bytes"]}, f, indent=1)
            f.write("\n")


if __name__ == "__main__":
    main()
