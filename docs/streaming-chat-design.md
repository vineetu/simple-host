# Streaming "Build with AI" chat — design (dev testbed)

**Status:** SHIPPED to https://simple-host.app/dev · 2026-07-16 (commits be8995f + 33c4d18).
Proxy go/no-go PASSED (SSE un-buffered, ~1.2s TTFT). Implementation reviewed by
Grok + Sonnet; P0 (false-completion on cut streams) fixed. Deferred items noted in
[[simple-host-streaming-chat]] memory. Draft history below.
**Owner:** Vineet · driver: Opus · reviewers: Grok (x-ai/grok-4.5), Sonnet

## Goal
Replace the opaque, blocking "Create a site with AI" build (currently a 2–3 min
spinner that says "doing something" then dumps the whole result) with a
**Lovable-style streaming experience**: first paint in ~1–2s, continuous visible
progress, staged status, and a live preview that fills in as the page is built.

Ship it on a **separate `/dev` page** so it can be dogfooded in isolation without
touching the live chat until it's proven.

## Decisions (from the user)
1. **DeepSeek direct path only.** Park the Claude Agent-SDK path (streaming through
   it is the hard case; DeepSeek is OpenAI-compatible and streams cleanly + cheap).
2. **Stream the response.** Token streaming, not a blocking request.
3. **Staged status, not a spinner** — "Designing → Writing the page → Done".
4. **Progressive live preview** — render partial HTML as it streams. *Stretch goal*;
   acceptable fallback is render-on-complete.
5. **Never show raw code inline.** But offer raw code as a **separate feature**
   (a "</> Code" view with copy/download), hidden by default.
6. **Allow typing while generating.** Don't lock the input; queue a message sent
   mid-stream and auto-send it when the current build finishes.
7. **Separate `/dev` page.** New embedded page; main `index.html` untouched.

## Non-goals (parked)
- Agent-SDK streaming.
- The free-ChatGPT "bring your own" fallback (separate future feature).
- Replacing the main `index.html` chat (only after `/dev` proves out).

## Current architecture (what we're changing)
- `POST /v1/generate` (in `internal/handler/generate.go`). Two backends:
  - **Agent path:** returns a `jobId`; client polls `GET /v1/generate/status` every
    2s; reveals only the final result.
  - **Direct path (DeepSeek/Claude Messages):** single **blocking** POST that returns
    `{reply, html}` all at once. No streaming.
- Model output format: reply text, then the `siteHTMLSentinel` marker, then the full
  HTML document. `splitReplyAndHTML()` splits on the sentinel; `cleanHTML()` strips
  markdown fences.
- Frontend (`index.html`): `sendChat()` shows a "• • •" bubble, awaits the whole
  response, then `updatePreview()` paints the HTML into a sandboxed `srcdoc` iframe
  (opaque origin, can't reach the parent's API key).

## Review synthesis (Grok + Sonnet, 2026-07-16) — changes to the plan
Both reviewers converged. Key revisions folded in below:
- **Don't paint partial HTML into the iframe.** Reassigning `srcdoc` per delta
  re-runs scripts, flickers, and can choke on unclosed tags. **Instead:** stream the
  HTML deltas into the **read-only "</> Code" panel** (which doubles as the "give me
  the raw code" feature *and* the "watch it build" feeling) and paint the iframe
  **once, on `done`**. This kills the biggest landmine and folds two features into one.
- **Client trusts `done` as the only source of truth** for the canonical
  `reply` + `html`. Deltas are **pure UX** (reply text + code-panel fill + stage pill).
  No reconciling half-parsed state → no flicker.
- **Stages are UX-only heuristics**, not a protocol: `connecting` → `generating`
  (first token) → `writing the page` (sentinel seen server-side) → `done`.
- **Proxy streaming is a GO/NO-GO GATE, step 0** — not a mitigation. If the
  ideaflow/nginx proxy buffers `text/event-stream`, progressive UX is off the table
  and we fall back to reply-only streaming or blocking. Prove it with `curl -N`
  through the proxy **before** building any UI.
- **Separate `http.Client` with no 120s timeout** for the streaming call (the
  existing `h.client` 120s cap would cut a long build); rely on `r.Context()` cancel.
- **`openapi.yaml` + regen `openapi.json`** for `POST /v1/generate/stream` is
  **step 1b** — `scripts/check-docs-sync.sh` hard-fails the deploy otherwise.
- **Wire the `/dev` route** explicitly in `main.go` (embedded `dev.html` does nothing
  until served).
- **Sentinel accumulator is new code** (~30–40 lines): `splitReplyAndHTML` is
  batch-only; the stream needs a stateful matcher that holds back the last ≤16 bytes
  (len(sentinel)-1) in case the marker spans a token boundary. `cleanHTML` runs on
  the final buffer only, never per-delta.
- **Pick ONE framing** — SSE (`text/event-stream`) — and stick to its Content-Type.
- **Defer queued-input + Stop** to the end; steps 1–4 already prove the value.
- Add `Stream bool json:"stream"` to the request struct.

## Proposed architecture

### Backend — new streaming endpoint
- **`POST /v1/generate/stream`** — auth-gated + rate-limited exactly like
  `/v1/generate`. Same request body (`{messages, html, attachments}`).
- Calls DeepSeek `chat/completions` with `stream: true`, parses the SSE token
  deltas, and **re-emits its own event stream** to the browser as newline-delimited
  JSON (NDJSON) or SSE frames:
  - `{"type":"stage","value":"designing|writing|finalizing"}`
  - `{"type":"reply","delta":"…"}` — chat text tokens (before the sentinel)
  - `{"type":"html","delta":"…"}` — HTML tokens (after the sentinel)
  - `{"type":"done","reply":"…","html":"…"}` — final canonical values (authoritative)
  - `{"type":"error","message":"…"}`
- **On-the-fly split:** accumulate the raw stream; watch for `siteHTMLSentinel`.
  Tokens before it → `reply` events + stage `designing`; tokens after it → `html`
  events + stage `writing`. Run `cleanHTML()` on the final buffer and send the
  canonical `done` so the client never trusts a half-parsed stream.
- **Transport:** chunked HTTP, `Content-Type: text/event-stream`, `flush` after each
  event, `X-Accel-Buffering: no` + `Cache-Control: no-cache`. New
  `converseStreamOpenAI()` in a `generate_stream.go` sibling; reuse
  `generateSystemPrompt`, the message-flattening, and `cleanHTML`.

### Frontend — new `/dev` page (`dev.html`, embedded)
- Standalone page served at **`/dev`**, reuses the same auth (localStorage `apiKey`).
  A focused clone of the chat, minus the JSON-paste toggle, plus:
  - **Stage pill** above the reply ("Designing…", "Writing the page…", "Done").
  - **Streaming reply bubble** — append `reply` deltas live.
  - **Progressive preview** — buffer `html` deltas; debounced (~150ms) `srcdoc`
    repaint once a `<head>`/`<body>` is seen; final repaint on `done`. *(Stretch.)*
  - **Input stays enabled.** A message sent mid-stream is **queued** and auto-sent
    when the stream ends (with a subtle "queued" chip). Optional "Stop" to abort.
  - **"</> Code"** button → slide-over panel with the HTML + Copy/Download. Hidden
    by default.
- Uses **`fetch()` + `ReadableStream`** (POST body + streamed response reader), not
  `EventSource` (which is GET-only and can't carry the messages body).

## Key risks (reviewers: please probe these)
1. **Proxy buffering.** The ideaflow/nginx proxy fronts every site and is known to
   mangle some things (it intercepts `/api`, breaks CORS preflight). If it **buffers**
   the response, streaming collapses back into a single blob. *Must test through the
   proxy early.* Mitigation: `X-Accel-Buffering: no`; fallback to NDJSON over chunked
   transfer, or a poll-based pseudo-stream if the proxy refuses to flush.
2. **Partial-HTML preview jank/flicker** — repainting `srcdoc` on every delta is
   janky and re-runs scripts. Mitigation: debounce; only start once `<head>` seen;
   consider swapping to a fresh doc only on structural boundaries.
3. **DeepSeek SSE edge cases** — `[DONE]` sentinel, role-only first delta, keep-alive
   comments, and reasoning models emitting thinking before content.
4. **Abort / lifecycle** — user navigates away or hits Stop mid-stream; queued
   message correctness; server context cancellation closing the DeepSeek stream.
5. **Sentinel split across token boundaries** — the marker may arrive split across two
   deltas; the accumulator must match on the joined buffer, not per-delta.

## Rollout
`/dev` only. Prove streaming + preview + proxy behavior, then port the engine into
the main `index.html` chat and (optionally) retire the blocking direct path there.

## Build order (for the plan) — revised
0. **GO/NO-GO:** stand up a minimal SSE endpoint, verify tokens stream with
   `curl -N` **locally AND through the ideaflow proxy**. If the proxy buffers →
   fall back to reply-only or blocking; do not build progressive UI.
1. Backend: separate `streamClient` (no 120s timeout), add `Stream bool` to the
   request struct, `converseStreamOpenAI()` (DeepSeek `stream:true`, `bufio` line
   reader, skip `: keep-alive` + `[DONE]`, extract `choices[0].delta.content`),
   stateful sentinel accumulator (hold-back window), reuse `generateSystemPrompt` +
   message-flattening + `cleanHTML` (final buffer only). Register
   `POST /v1/generate/stream` (authMW, ip/user limiters, **no** noticeMW).
   1b. Update `openapi.yaml`, regen `openapi.json`, run `scripts/check-docs-sync.sh`.
2. Wire `/dev` route in `main.go`; add embedded `dev.html`.
3. `dev.html`: streaming **reply** bubble + **stage pill** (no preview yet) via
   `fetch()` + `ReadableStream`. Client trusts `done` for canonical values.
4. **"</> Code" panel** streams the HTML deltas (read-only); paint the iframe **once
   on `done`**. (This is the "watch it build" feel + the raw-code feature, together.)
5. Queued input + Stop (frontend state machine: `idle|streaming|queued|aborting`).
6. Only after `/dev` proves out: port the engine into the main `index.html` chat.
