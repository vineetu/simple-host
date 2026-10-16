---
name: simple-host-brain
description: Read this skill first whenever the owner of Simple Host asks about Simple Host, Simple Hack, Simple Host Enterprise, the self-hosted editions or Simple Hack for companies: what a product is for, whether a feature exists or how it works, which edition has it (parity), what was decided and why, or what changed recently. It answers from the owner's private product knowledge base instead of memory. Owner only.
---

# Simple Host product knowledge (owner only)

The owner's private knowledge base at `https://simple-host.app/v1/brain`, rebuilt every hour and after
every deploy from the INTENT, FEATURES, PARITY and CHANGELOG files of both repos, plus design,
platform and release docs. It only reads. It answers only the owner's account; anyone else gets 403.

Ask it before answering any question about these products. Quote what it returns and name the source
file. If it does not say, say so rather than guessing.

## Sign in once

You need the owner's Simple Host API key, sent as the `X-API-Key` header. If you already keep one for
simple-host.app, use it. Otherwise sign in as the owner:

1. Ask the owner for the email of their Simple Host account.
2. `POST https://simple-host.app/v1/auth` with `{"email":"<that email>"}`. They get a 6-digit code.
3. Ask them for the code, then `POST https://simple-host.app/v1/auth/verify` with
   `{"email":"<email>","code":"<code>","choose_handle":true}`. The answer carries `api_key`.
4. Keep the key in your own secret store. Never show it, and never put it in a page or a file you share.

A 403 with `owner_only` means the key belongs to some other account: only the owner's works.

## Ask

`GET https://simple-host.app/v1/brain` lists the tools with their arguments and says when the
knowledge was built. Each tool is `POST https://simple-host.app/v1/brain/<tool>` with a JSON object of
arguments and `Content-Type: application/json`; the answer is `{"text": "...", "result": {...}}`.

| Tool | Use it for | Arguments |
|---|---|---|
| `product_overview` | what a product is, who it is for, non-goals, current version, freshness | `product`: host, hack, enterprise, selfhost-host, selfhost-hack, hack-companies or all |
| `feature_lookup` | does a feature exist, how it works, its parity row | `query` |
| `parity` | which edition has a feature, or the whole table | `feature` (optional) |
| `decisions` | dated owner decisions and their reasons | `query`, `since` (2026-10-13 or 3d), `product`, `limit` (all optional) |
| `recent_changes` | what changed lately (default the last 7 days) | `since`, `product`, `limit` (optional) |
| `search` | full-text search over every source | `query` |
| `get_doc` | read a document or one section | `path` such as `simple-host/PARITY.md`, `section` (optional) |

Example: "Does Enterprise have the WordPress importer?" is
`POST /v1/brain/parity` with `{"feature":"WordPress importer"}`.

In parity answers, "hosted" means simple-host.app and the small box (the same binary, which also runs
Simple Hack) and "enterprise" is the Kubernetes edition. "gap → enterprise" means Enterprise should get
it; "gap → hosted" means the hosted side should.

Apps that take an MCP connector (Claude, ChatGPT, Grok, Meta Muse) use
`https://simple-host.app/brain/mcp` instead, with the same tools.
