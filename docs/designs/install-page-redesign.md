# Install / Get-Started page redesign

**File:** `internal/handler/static/install.html` (embedded in the Go binary → rebuild + redeploy to take effect).
**Problem:** the page has too much information. It explains the skill twice, offers ~6 ways to install it, stacks three audiences vertically with no self-selection, duplicates the homepage's "what you can build" cards, and pushes `llms.txt` into the spotlight. New visitors can't tell what to do.

## Goal

One glanceable page, three clear paths, each with exactly **one** action, and the right path on top for whoever is looking (device-aware). `llms.txt` keeps working but is never named or featured.

## Positioning

The page's unique job vs. the homepage: the homepage already *has* the interactive "Build with AI" chat and the "paste JSON" box. So this page leads with the skill on desktop (the thing the homepage can't do), but a phone can't install a skill — so on mobile it leads with the on-site chat.

## New structure (top → bottom)

Universal hero: headline ("Ship a website by talking to your AI") + one-line lede. No `llms.txt` URL bar.

Then three compact path blocks, each: icon + title + one sentence + ONE action.

1. **💬 Build with AI — right here.** "Describe it, we build and preview it. No setup — works on your phone." Button → the on-site create-with-AI chat on `/`. *Hero on mobile.*
2. **⌨️ In a coding agent? Add the skill.** One recommended line: `npx skills add vineetu/simple-host`. A single `<details>` **"Other tools · manual · Windows"** hides: the per-tool folder table (the one keeper from the old second section), the curl macOS/Linux + PowerShell Windows unzip, and the `install.sh` / Hermes / OpenClaw notes. Keep ONE example-prompt terminal mockup here. *Hero on desktop.*
3. **📋 Prefer ChatGPT / Gemini? Copy the prompt.** One "Copy prompt" button (reuses the existing `copy-llms` JS that fetches `/llms.txt` — relabeled, never named). One sentence: paste into your chat → it returns a JSON block → paste it back at simple-host.app and Publish. Tiny inline note: publishing needs a one-time email sign-in.

## Device flip

Pure CSS: the three blocks are order-able flex/grid siblings. Default (desktop) order = 2, 1, 3 (skill first). At `max-width: 640px`, `order:` puts block 1 (chat) first, then 2, then 3. No JS, no flicker.

## Removed

- The entire second "Installing the skill" section (its table folds into block 2's `<details>`).
- The standalone web-chat section + its second mockup (folds into block 3).
- Prominent `llms.txt`: the hero `llms-line`, the `/llms.txt` links, the "Follow …/llms.txt" line in the mock. The copy button + its JS stay (still fetch `/llms.txt`), just relabeled.
- The "what you can build" cards (duplicated from the homepage) — drop, or keep one slim strip. Default: drop.

## Out of scope

`llms.txt` itself stays served. Other pages that reference it (README, docs.html, architecture.html, api-catalog, auth error strings) are unchanged — this is the install page only.

## Testing

- Renders clean at desktop, tablet, and ≤640px; on a phone width the chat block is first, skill second.
- Both light/dark themes intact.
- "Copy prompt" copies the full instructions; skill copy buttons work; all links resolve; the on-site-chat CTA lands on the create flow.
- `/llms.txt` still returns 200 (endpoint untouched).
- Rebuild binary (go124), `check-docs-sync.sh` passes, deploy to prod, spot-check live.
