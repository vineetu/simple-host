Adversarially review the hackathon feature set on branch `feat/hackathon-instances` in this repository. Report findings only. Do NOT edit any file. For each finding give file:line, a concrete failure scenario with inputs, and severity. Full coverage; do not filter to high severity only. If something is fine, say so in one line rather than padding.

You cannot see the conversation that produced this, so here is what it is and what it claims.

# What this is

Simple Host is an open-source Go server that hosts static websites and gives each site a small JSON backend. It runs live at simple-host.app.

This branch lets a hackathon organiser run a private instance on their own cloud server. The design decisions, and the reasons, are written up in `docs/HACKATHON-PLAN.md` — read it first.

The flow: an organiser's AI coding agent installs a skill, creates a server on the organiser's own cloud account, claims two hostnames from simple-host.app pointing at that server, runs an install script over SSH, then creates participant accounts in bulk and hands out API keys. Participants publish with their own agent. Afterwards the agent deletes the server and releases the hostnames.

Participants never sign in. That is deliberate: email codes need a provider with a 100/day free cap, and Google needs an OAuth client per hostname.

# What to scrutinise, hardest first

## 1. `internal/handler/eventdomain.go` and `internal/eventdns/`

This lets **any authenticated account** create real public DNS records under simple-host.app and agent-deploy.dev, pointing anywhere they choose.

- Can a caller point a hostname under our domain at an IP address they do not control? What is the worst thing that enables? Consider phishing, cookie scope, certificate issuance by a third party, and reputation.
- Is there any rate limit? What stops one account claiming hundreds of names?
- `NormalizeName` and `ValidatePublicIP`: can anything slip through? Consider IPv4-mapped IPv6, decimal or octal IPv4 forms, leading zeroes, trailing dots, unicode, and names that are valid labels but dangerous as hostnames.
- The claim path inserts a row, then creates records, then updates the row with record ids. Trace every failure point. Can a record exist that no row tracks, or a row that names records which no longer exist? What happens on two concurrent claims of the same name?
- The expiry sweep deletes records for expired claims. Can it delete records belonging to a claim that was just renewed? Can it loop forever on a permanently failing record?
- Is the reserved-name list sufficient for the domains actually in use? Check the real records on those zones if you can reason about it.

## 2. `internal/handler/accounts.go`

Bulk account creation, deletion, and profile edits.

- `createAccounts` runs inside one transaction and calls `auth.GenerateAPIKey` per account. If the transaction rolls back after keys were returned to nobody, is anything inconsistent? Are keys ever disclosed for an account that already existed?
- `deleteAccount` deletes the database row, commits, then deletes files from disk. Argue about that ordering. What is the exposure window? What happens if the process dies between them?
- `patchMe` allows a handle change only when the account owns zero sites, using `SELECT ... FOR UPDATE` on the users row and then a separate existence check on `sites`. **Is that lock actually sufficient to prevent a concurrent first deploy?** Find what the deploy path locks and whether the two orderings can interleave. This is the finding I most want checked.
- Can a non-admin reach any admin endpoint? Can an admin delete themselves through any path?

## 3. `internal/handler/instancehost.go`

Rewrites the literal hostname `simple-host.app` in served assets so an instance on another domain describes itself. Nil on simple-host.app so production is untouched.

- Can the substitution corrupt anything? It runs over every file in the skills zips via `copyRewritten`, including any non-text file.
- Is the ordering safe for every input, including a hostname inside JSON, YAML, a URL with a port, or split across a chunk boundary?

## 4. `deploy/install/install.sh`

Runs as root on a fresh Ubuntu server, fetched over HTTPS and piped to bash by an agent.

- Argue about that trust model honestly.
- Is it genuinely idempotent? Trace a re-run after each possible failure point.
- It preserves the admin key and database password across re-runs by grepping its own `.env`. Can that parse wrongly? What if a value contains an `=` or a newline?
- Are the generated secrets strong enough, and generated safely?

## 5. `simple-host-website/skills/run-hackathon/`

Instructions an AI agent follows unattended with the organiser's cloud credentials.

- Is anything ambiguous enough that a capable agent could do something destructive or expensive?
- Does it ever tell an agent to publish to simple-host.app instead of the event's own instance? That would send a participant's work to the wrong server.
- Does what it promises match what the code actually does?

## 6. `internal/analytics/ingest.go`

Now parses Caddy's JSON access log as well as the nginx tab-separated format.

- Can a visitor forge a log line and so forge analytics? Consider a crafted User-Agent, path or header.
- Is the nginx path provably unchanged? 400 days of retained log is replayed by a rebuild tool.

## 7. `.github/workflows/`

The repository is public, so a fork can open a pull request.

- Any script injection through `${{ }}` interpolation, branch names or tag names?
- Are token permissions minimal?

## 8. The pages

`internal/handler/static/hackathons.html` claims things about the product. Check every factual claim against the code. Flag anything the code does not actually do. Ignore `enterprise.html`; it is being handled separately.
