Write TWO new files. Do not modify any existing file; I will do the wiring myself.

1. `internal/handler/setup.go`
2. `internal/handler/static/setup.html`

Read these first so you match the codebase: `internal/handler/eventdomain.go`, `internal/handler/accounts.go`, `internal/handler/ui.go`, `internal/handler/static/features.html`, and `docs/ONE-CLICK-PLAN.md`.

Run `gofmt`, `go build ./...` and `go vet ./...` before you finish. Report in under 150 words.

# The situation

Simple Host now ships a Docker Compose file. Someone installs it from a provider's catalog or by running one command. The box boots knowing nothing about where it lives: it has no hostname, only a public IP address that the provider shows the person who created it.

They open `http://<that-ip>/` and this page is what they see. It is the only thing standing between a running container and a working instance.

**The person reading it is not technical.** They have never edited a DNS record. They may not know what a DNS record is. They are probably running an event tomorrow. They will not read a wall of text, and if the page looks like documentation they will close it.

# The backend: `setup.go`

Package `handler`. Type `SetupHandler` with `NewSetupHandler(db *sql.DB, publicAPI string, password string) *SetupHandler` and `Register(mux *http.ServeMux)`.

`publicAPI` is the base URL of the public Simple Host instance, for claiming free hostnames. `password` is an optional setup password; empty means no password is required.

Routes:

- `GET /` and any unmatched path — serve `setup.html`.
- `GET /v1/setup/state` — `{"configured": bool, "needs_password": bool}`.
- `POST /v1/setup/verify` — body `{"password": "..."}`. 204 if it matches or none is set, 401 otherwise. Rate limit this: use `newRateLimiter` from `ratelimit.go`, keyed by remote IP.
- `POST /v1/setup/own-domain` — body `{"domain": "hack.example.com"}`. Validates, returns `{"host": ..., "content_host": ..., "records": [{"name": ..., "type": "A", "value": "<this box's public IPv4>"}, ...]}`. Does NOT save yet.
- `GET /v1/setup/dns-check?domain=...` — resolves both hostnames and reports `{"host_ok": bool, "content_ok": bool, "ip": "..."}` so the page can poll while the person adds records.
- `POST /v1/setup/free-name` — body `{"api_key": "...", "name": "stanford-cs-2026"}`. Calls `POST {publicAPI}/v1/events` with that key and `{"name":..., "ip": <this box's public IPv4>}`. Relays the upstream error text on failure, because "that name is taken" must reach the person unchanged.
- `POST /v1/setup/finish` — body `{"host": ..., "content_host": ...}`. Writes both to a new table `instance_config (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TIMESTAMPTZ NOT NULL DEFAULT now())` and returns `{"admin_api_key": "..."}` read from the environment. **Refuses if `site_domain` is already set**: setup happens once, and a second run would move a live instance and strand every URL anyone published. Use a transaction with `FOR UPDATE`.

Also export `func InstanceConfigured(ctx context.Context, db *sql.DB) (siteDomain, contentHost string, err error)` so `main.go` can decide at boot whether to run in setup mode. Missing rows are not an error.

Discovering this box's own public IPv4: try the cloud metadata services first, then fall back to an HTTPS echo service. Cache the result. Say in a comment why a metadata service is tried first.

Add the `instance_config` table to `db/schema.sql` and a matching `db/migrations/instance-config.sql`. That is the one existing file you may touch.

# The page: `setup.html`

Self-contained. Inline CSS and JS. No build step, no external JavaScript. Google Fonts by link tag is fine.

Use the existing Simple Host look: dark background, the same fonts and the cyan accent, as in `features.html`. It is not my favourite but consistency beats novelty today.

## How it must behave

**One question on screen at a time.** Never two. Never a form with five fields.

The whole flow, in order:

1. Password, only if `needs_password` is true. One field.
2. "Do you have a domain?" Two large buttons. Nothing else on the screen.
3a. **No domain:** ask for an event name, and for their simple-host.app key with a link to where they get it. Then it just works, because the box claims the names itself.
3b. **Yes:** ask for the domain, then show the two records **as a table they can copy**, and poll `dns-check` on a timer. Show plainly that it is waiting and that DNS often takes a few minutes. Continue automatically the moment both resolve. Never ask them to click "I've done it".
4. Wait for certificates.
5. **Done.** This screen matters most. It shows:
   - The admin key, once, with a copy button, and a line saying it will not be shown again.
   - **The prompt a participant pastes into their AI agent, with a copy button.** Shape:
     ```
     Read https://<host>/llms.txt and follow it exactly.
     My Simple Host API key is: <their key>
     Ask me what I want to build, then build it and publish it.
     ```
   - A link to the new instance.

## Rules for the writing on it

- **Fewer than 30 words per screen.** Count them.
- No jargon without a plain-language gloss. Not "A record" alone, but a table with the values and one line saying where to put them.
- Errors say what to do next, never just what failed.
- No emoji, no exclamation marks, no marketing tone.
- Every button says what it does. Never "Submit", "Continue" or "Next" alone.

## Rules for the interaction

- Copy buttons on the admin key, on the prompt, and on each DNS value.
- Disable a button while its request is in flight and show that it is working.
- If a request fails, keep everything they typed. Never make them type it twice.
- Works on a phone. An organiser may be doing this from one.
- The page must be usable with a keyboard alone.

# What NOT to build

- No account creation. That happens later, on the admin page.
- No fetching the admin key from anywhere but the environment.
- No storing the person's simple-host.app key. It is used for one call and discarded.
- No JavaScript framework, no bundler, no npm.
