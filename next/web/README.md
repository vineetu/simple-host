# web: the service's own pages

- `static/` plain pages served as they are (home, install, docs, terms, privacy, support, the dashboard for now); photo-process.js is shared by the upload screen and the in-chat photo picker.
- `app/` the **owner app** source: React + TypeScript, Vite, Tailwind, Radix (shadcn-style components in `src/components/ui`), TanStack Query and Table, React Router, vite-plugin-pwa. Screens: `/signin`, `/admin`, `/upload/<token>` (the page behind an upload link).
- `dist/files/` the **committed build** of `app/` (Go embeds it; no Node needed to build the service), plus `pages.css`, the stylesheet of the gate, status and text pages, made from the same tokens.
- `DESIGN.md` the rules every screen follows.

## Commands (from `next/`)
| | |
|---|---|
| `make web` | rebuild `dist/files` (commit it with the source change) |
| `make web-check` | type-check, lint, unit tests, a fresh build compared with `dist/files` (fails if stale), every catalogue entry at 390 and 1280 px: console errors, sideways scroll, cut-off text, tap targets under 44 px, axe, and the picture against `app/tests/baseline/` (about 2 minutes) |
| `make web-accept` | write new baseline pictures after a deliberate change; look at them before committing |
| `make web-real` | the real binary on a throwaway database, signed in and clicked through at both widths; pictures in `app/test-results/real/` |
| `cd app && npm run dev` | live reload while working; `/catalogue.html` shows every screen against fixtures |

## The catalogue
`app/src/catalogue/entries.ts` lists every screen in every state: a URL, the API's answers (fixtures in `fixtures.ts`) and steps to reach a dialog or step. `catalogue.html` shows them all side by side; `catalogue.html?id=<id>` one alone. The clock is frozen at `NOW` for the pictures.

The pages the Go server writes (gates, status, the four text pages) are in the catalogue too (`app/src/catalogue/pages.ts`): `make web-pages` renders the gate pages from `sites/core/gate.go` into `app/.catalogue-pages` (web-check and web-accept run it first), and the catalogue build copies them and the text pages to `/catalogue-pages/`.

## How to add a screen
1. Read `DESIGN.md`. Make `app/src/screens/<area>/<Screen>.tsx` from `components/ui` pieces only, inside `AppShell` (signed in) or `PageShell` (signed out).
2. Fetch with `useApi()` + TanStack Query; put the route's types and hooks in `screens/<area>/data.ts`. Unit-test any plain logic beside it (`*.test.ts`).
3. Add the route in `src/main.tsx` (lazy, like the others) and in `src/catalogue/main.tsx`; serve the path from Go by adding it to `appPages` in `web.go`.
4. Add catalogue entries for every state: loading, empty, loaded, error, each dialog, each form error. Fixtures go in `fixtures.ts`.
5. `make web-accept`, then look at every new picture in `app/tests/baseline/` at both widths and fix what looks wrong; repeat until `make web-check` passes.
6. `make web`, then commit source, baselines and `dist/files` together; extend `app/tests/real.spec.ts` if the screen changes data, and run `make web-real`.
