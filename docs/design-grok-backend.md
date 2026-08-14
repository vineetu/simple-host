# Design: Grok CLI as the primary generation backend, DeepSeek as fallback

Status: DRAFT v1, for adversarial review. Not implemented.

## Goal

`/v1/generate` currently calls DeepSeek over an OpenAI-compatible HTTP API. Make
a **local `grok` CLI invocation** the primary generator, and fall back to
DeepSeek when it fails. No xAI API key exists on this box; the `grok` CLI is
authenticated with an OAuth device credential under `/home/ubuntu/.grok`, which
cannot call `api.x.ai/v1/chat/completions`. Hence CLI, not HTTP.

## Measured baseline (this box, aarch64, 4 cores)

| | grok CLI | DeepSeek HTTP |
|---|---|---|
| coffee-shop page | 39s, $0.0065, 1 turn | ~85–130s |
| tools disabled | wrote no files | n/a |
| output shape | reply + `<<<SITE_HTML>>>` + doc | same |

Probe: `--max-turns 3 --effort low --no-plan --no-subagents --no-memory
--disallowed-tools run_terminal_cmd,search_replace,read_file,list_dir,grep,web_search,web_fetch,Agent`

## Architecture

Add a third backend to the existing dispatch in `generate()`, ahead of the
OpenAI-compatible path:

```
grokCLI configured?  -> run grok, on failure fall through
llmKey set?          -> DeepSeek (existing) — now also the FALLBACK
agentURL set?        -> agent server (existing)
anthropic key?       -> Messages API (existing)
```

Both primary and fallback run inside the SAME background job (see
`generate_jobs.go`), so the client's poll contract is unchanged and no request
becomes long-lived. Fallback is invisible to the client except in timing.

### Invocation

```
grok --cwd <ephemeral dir> --prompt-file <tmp> --output-format json
     --max-turns 2 --effort <cfg> --no-plan --no-subagents --no-memory
     --disallowed-tools <everything>
```

- Prompt goes via `--prompt-file`, never argv: it is untrusted user text and
  must not touch a shell command line.
- `--cwd` is a fresh empty dir per request, removed afterwards.
- The existing system prompt (site-building instructions, `<<<SITE_HTML>>>`
  contract, current-HTML block) is prepended into the prompt file, since the CLI
  has no separate system-prompt channel.

### Security — the central risk

`/v1/generate` accepts arbitrary text from any signed-in user. Feeding that to an
*agentic* CLI on the box that hosts 51 sites, jot-transcribe.com, Jellyfin and
the donations service is a prompt-injection-to-RCE path if tools are live.
Controls, defence in depth:

1. **Every tool denied.** `--disallowed-tools` lists all known tool IDs, and
   `--deny` rules as a second layer. Never `--yolo`.
2. **No shell.** Go builds an `exec.Cmd` argv directly — no `sh -c`.
3. **Dedicated unix user** with no write access to site content, and its own
   `$HOME`. Runs under `systemd-run --scope` with `PrivateTmp`, `ProtectSystem=strict`,
   `ProtectHome`, `NoNewPrivileges`, and a read-only bind of the credential dir.
4. **Ephemeral cwd** under that user's private tmp.
5. **Hard timeout** and process-group kill, so a wedged agent cannot linger.
6. **Concurrency cap** — one CLI process is not free; reuse the job store's
   existing per-user (3) and global (64) ceilings, plus a smaller cap specific
   to CLI processes.

### Credential problem (open)

`grok` reads `/home/ubuntu/.grok/auth.json`, owned by `ubuntu`. `simple-host`
runs as `simplehost`. Options:
- (a) copy the credential into a service-owned dir, readable only by it;
- (b) run the CLI via `systemd-run --uid=ubuntu`;
- (c) a dedicated `grokgen` user with its own device login.
(a) is simplest; (c) is cleanest but device login needs a human at first setup
and on any re-auth. **Token refresh is the risk in all three**: the CLI refreshes
tokens by writing back to `auth.json`, so the file cannot be read-only, and two
processes refreshing concurrently may race.

### Progress

`--output-format streaming-json` emits events as the agent works. Map those onto
the existing `report(string)` callback so the thinking bubble keeps moving.
NOTE: the CLI does not expose token deltas the way the DeepSeek stream does, so
progress will be coarser — likely "Thinking…" then a size counter once the HTML
block starts arriving. The `reasoning_content` behaviour does not apply here.

### Fallback policy

Fall back to DeepSeek when the CLI: exits non-zero, exceeds its timeout, returns
`stopReason` other than `end_turn`, or returns text with no `<<<SITE_HTML>>>`
sentinel when a build was expected. Do NOT fall back on a user-caused error
(e.g. context too long) — that wastes a second full generation for the same
outcome. Log which backend served each turn.

### Config

```
GROK_CLI_PATH=/home/ubuntu/.local/bin/grok   # empty disables, DeepSeek stays primary
GROK_CLI_EFFORT=low
GROK_CLI_TIMEOUT=300s
GROK_CLI_HOME=/var/lib/grokgen
```

## Explicitly out of scope

Switching the *voice* path or the vision pass; both stay as they are.

## Known weaknesses to attack in review

- Cost/latency per build vs DeepSeek under real load, not a one-shot probe.
- Token refresh races and what happens when the device credential expires —
  does a build fail closed to DeepSeek, or hang?
- Whether `--disallowed-tools` genuinely blocks every tool, including ones added
  in a future CLI version. A version bump could silently re-enable something.
- Whether the agent can be prompted into emitting a page that is itself harmful
  (it is served from our domain).
- Memory/process footprint of N concurrent CLI processes on a 4-core box that is
  already running Postgres, Jellyfin, Hermes and a 1GB speech model.
