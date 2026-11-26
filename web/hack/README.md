# web/hack: rebuilding a Simple Hack screen

Rules: `next/web/DESIGN.md` (Hack keeps its red accent; values in `src/styles/tokens.css`, names checked against Host's). Screen ids are `navigation.json`'s (`routes/resolve.ts`). Each family owns `src/screens/<area>/` and touches nothing else.

1. **Write it.** `src/screens/<area>/<Screen>.tsx`, default export, props `ScreenProps` (`match.slug`, `match.section`). Use only `@kit/components/ui`, `app/states` (Loading, Failed), `shared/*`. Call the service through `useHack()` (`src/lib/client`). Plain logic goes in `data.ts`, with tests in `data.test.ts`.
2. **Register it.** Add it to `src/screens/<area>/routes.ts`: `"<screen-id>": { kind: "event" | "home" | "public", title, load: () => import("./X") }`. Event screens get the frame for free: event loading, role check, pending lock, not-found, rail, status strip.
3. **Catalogue.** Every state goes in `src/screens/<area>/catalogue.ts` with ids `<screen-id>.<state>`: loading, empty, error, loaded, and every dialog or step. Fixtures come from `src/catalogue/fixtures.ts`'s `api({...})` (override paths in your own file). Both widths are automatic.
4. **Switch it on.** Add the id to `src/screens/<area>/live.json`. The build writes `screens.json` and the Go server serves the new page for that screen (`products/hack/pages/hack_ui_switch.go`). Both apps hop to each other with a full page load. Leave the id out to ship the code dark.
5. **Check.** From the repo root:
   - `make hack-web-accept`, then look at every new picture in `tests/baseline/`.
   - `make hack-web-check` (typecheck, lint, vitest, stale dist, catalogue at 390/1280 with axe and no bold on a phone).
   - Extend `tests/real.spec.ts` for any flow that changes data, then run `make hack-web-real`.
   - `make hack-web`, then commit source, baselines and `products/hack/web/dist/files` together.
   - `go test ./products/hack/pages`.
6. **API parity.** Every route SCREENS.md lists for the screen is called from your code, or the report lists it as dropped.

**Shared files.** Change these only by adding, and keep a family's lines together.
- `src/lib/client/<area>.ts`: add functions, and type payloads in place. Never rename. A new area needs one import line and one key in `client/index.ts`.
- `src/shared/*`, `src/app/*`, `src/lib/*`, `web/kit/*`: a missing primitive or a frame change goes back to the foundation owner. Do not edit in place. Kit edits change Simple Host's build, so run `make -C next web-check`.
- `products/hack/pages/hack_ui_switch.go`: never per screen.

**Not edited per screen** (globs pick each family up):
- `routes/registry.ts` for `screens/*/routes.ts`
- `catalogue/main.tsx` and `tests/entries.ts` for `screens/*/catalogue.ts`
- `vite.config.ts` for `screens/*/live.json`
- `navigation.gen.ts`, which is generated: run `python3 scripts/build-hack-navigation.py`

Dev: `cd web && npm run hack:dev`, then open `/catalogue.html`.
