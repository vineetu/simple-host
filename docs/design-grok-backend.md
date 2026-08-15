# SUPERSEDED — not implemented, and deliberately so

Grok is the builder model as of 2026-08-15, but **not by this design**. It is
reached through a local CLIProxyAPI sidecar that turns the existing Grok
subscription into an ordinary OpenAI-compatible endpoint, so generation is a
plain chat-completions call: no agent on the host, no tools, no sandbox to get
right. See the commit "Run the builder on Grok, with DeepSeek as fallback".

This document is kept because the reviews that killed it are worth reading — it
went through three rounds and each round found the previous version unsafe or
unrunnable. Do not implement what follows.

---

# Design: Grok CLI as a generation backend, DeepSeek as fallback

Status: DRAFT **v3**, after two rounds of independent adversarial review
(security / ops / integration). Not implemented.

## Revision history — why the shape kept changing

**v1** had `simple-host` exec `grok` directly, sandboxed with `systemd-run`,
safety resting on `--disallowed-tools`. Rejected: `simple-host.service` runs
`ProtectHome=true`, `RestrictNamespaces=true`, empty caps, no `AF_UNIX`, no
sudo — it cannot see the binary or credential, and cannot reach systemd. It
would have silently fallen through to DeepSeek forever. Worse, a child it *could*
spawn inherits `ADMIN_API_KEY`/`DB_DSN` and write access to every hosted site.

**v2** moved the CLI into a separate `grokgen.service` and made tool denial
"allowlist-first, fail-closed". Rejected on two counts, agreed by all three
reviewers independently:

- The "assert the tool list is empty on the first streaming event" check is a
  **detector, not a lock**. `read_file` of a small file completes in the same
  breath as the `tool_call` line; killing afterwards is too late — the bytes are
  already in the model context and in the session transcript. And on
  `streaming-json` there is no leading tool advertisement to assert on at all.
- `--tools` empty does not produce a tool-less agent, and `dontAsk` is not a
  deny. Read-only tools run in **every** permission mode unless a deny rule
  matches by exact ID, and **unrecognised IDs in rules are skipped with a
  warning** — so the list fails open by construction, and a CLI version bump can
  add a tool the list never heard of.
- The 150s grok slice cannot finish a real build (the only measured number, 39s,
  was an empty probe; real turns carry ≤96 KB of current HTML plus history), and
  then leaves DeepSeek too little of the 8-minute job to be a safety net.

## v3 principle: the sandbox is the boundary, not the tool flags

Assume the agent **can** call tools, because we cannot prove otherwise across
versions. Make that harmless. Tool flags stay, as defence-in-depth and cost
control, but nothing rests on them.

### What the CLI must not be able to reach

| Asset | Control |
|---|---|
| `/etc/simple-host.env` (`ADMIN_API_KEY`, `DB_DSN`, …) | `640 root:simplehost`; **`grokgen` is not in that group**. Also never in its environment. |
| `/srv/simple-host/sites` — every tenant's live HTML | `InaccessiblePaths=` (or `TemporaryFileSystem=/srv`). Invisible, not merely unwritable. |
| `/home/ubuntu` (workspaces, `ubuntu`'s own grok credential) | `ProtectHome=true` |
| The rest of the filesystem | `ProtectSystem=strict`, `PrivateTmp=true`, `ReadWritePaths=/var/lib/grokgen` only |
| Escalation | `NoNewPrivileges=true`, empty `CapabilityBoundingSet`, `RestrictNamespaces=true` |

The service also carries `MemoryMax=2G`, `TasksMax=`, `KillMode=control-group`,
`TimeoutStopSec` ≤ budget, so nothing is orphaned across a restart.

**Custom sandbox profile must fail closed.** The CLI's built-in `strict` sandbox
*warns and continues* if it cannot apply — that is fail-open. Use a profile that
refuses to start, and if it cannot be applied, do not run the backend.

### Residual risk, stated plainly

Even sandboxed, the agent can read its own `HOME` (including its own
credential), the base OS, and can reach the network (it must, to call xAI). So:

- Session transcripts under `/var/lib/grokgen/.grok/sessions/` retain tool
  results — **disable session persistence, or wipe per request**.
- Kill the process and **discard stdout** on any `tool_call` / `tool_use` event.
  This does not prevent the read; it prevents the read becoming a *reply*, which
  is the exfiltration channel that matters.
- `--max-turns 1`. One turn cannot read-then-report.
- Egress restriction to xAI endpoints only would close network exfil. Deferred:
  needs per-uid firewall rules, and is not required if the sandbox holds.

This is a smaller blast radius than v1 by a wide margin, but it is **not zero**,
and that is the honest trade for having no API key. An `xai-…` key from
console.x.ai removes this entire attack surface, because generation becomes a
plain HTTPS call with no agent and no host access.

### Time budget: preflight, then a single owner

No mid-flight handoff. That was the flaw in v1 and v2.

```
1. Preflight, ≤10s total, before any generation:
     - pinned version matches
     - credential valid (not expired / not awaiting device re-auth)
     - advertised tool set as expected
   Any miss  -> skip grok entirely; DeepSeek gets min(7m, remaining budget).

2. If preflight passes, grok OWNS the job, up to jobRunTimeout.
   Fallback applies only to fail-fast errors that occur BEFORE the first model
   token (non-zero exit, auth/quota, immediate protocol error).

3. Once grok has emitted a token there is no fallback. It finishes or the job
   fails. Starting DeepSeek at that point cannot fit in the remaining budget and
   bills the user twice for one answer.
```

Ordering invariant is unchanged and must hold:
`provider timeout ≤ remaining budget < jobRunTimeout (8m) < client poll (9m)`.

Note the vision pass for attachments runs **inside the same job** before a
backend is chosen; the remaining-budget calculation must subtract it.

### Integration fixes (unchanged from v2, all still required)

- `cmd/server/main.go` gates `/v1/generate` on
  `LLMAPIKey || AgentServerURL || AnthropicAPIKey` — add the grok backend or a
  grok-only config 404s.
- `usesLocalJobs()` must treat grok as local, or polls are proxied to the agent
  server and every build reports as expired.
- Reuse the **existing** prompt assembly (system prompt + date + ≤96 KB current
  HTML + history + inlined attachments), written to `--prompt-file`, never argv.
- One output format throughout. `streaming-messages-json` is the only one that
  leads with `init.tools`, so it is the choice if any assertion is kept.

### Config

```
GROKGEN_URL=http://127.0.0.1:8101/generate   # empty disables; DeepSeek stays primary
GROKGEN_PINNED_VERSION=1.0.3
GROKGEN_PREFLIGHT_TIMEOUT=10s
```

## Open questions for round 3

- Does a one-time device login as `grokgen` survive unattended operation, and
  what is the re-auth runbook when it expires?
- Does copying the binary out of `~/.local/bin` break its bundled assets?
- Can session persistence actually be disabled, or must it be wiped per request?
- Is "kill and discard stdout on tool_call" implementable against the chosen
  stream format without racing the process's own exit?
