# Product docs

Source MDX lives in `src/content/docs`. Run `bash scripts/build-docs-site.sh` from
any directory with Node 22.23.4, npm, and Python 3. Commit the
source, lockfile, and `internal/webassets/docs-site` together. Go needs no Node.
On the shared development box, run the build under `systemd-run` with MemoryMax=3G.
CI uses Node 22.23.4 and `npm ci`, rebuilds both brands, and rejects output drift
outside `pagefind/`. Pagefind's arm64 and x64 binaries are both pinned to 1.6.0,
but embed platform-specific WASM and produce different index hashes. Manifest
hashes and byte counts exclude that directory. `python3 scripts/docs-site.py check`
verifies source/output hashes without Node, plus each brand's Pagefind directory,
entry JSON, indexed HTML page count, and referenced metadata/WASM files. The same
check runs on freshly rebuilt CI output.

## Generated reference pages

`/docs/api/` and `/docs/mcp/` are not hand-written. `scripts/docs-reference.py`
(called from `docs-site.py prepare`) runs `go run ./scripts/inventory tools-json`
and `openapi-json` for the brand and writes plain `.md` pages (not MDX, so stray
`<` and `>` in descriptions are safe once escaped) under `.generated-content/api`
and `.generated-content/mcp`, plus `reference.json`, which `astro.config.mjs`
reads to build the collapsed Reference sidebar groups. Each operation and tool
carries a `data-operation`/`data-tool` marker exactly once per brand tree;
`scripts/check-docs-site.py` enforces that, plus internal link health and the
owner rule that the Host build never says "hack" (the generator drops Hack
parentheticals and sentences from Host text, trimming a two-product sentence
down to its Host half rather than deleting it outright). The manifest also
tracks a digest of the four inventory JSON outputs, so `docs-site.py check`
catches a route or tool change that never made it into a rebuild.

## Write a page

Use plain task headings, numbered Steps, and a `What` feature link under each task
H2. Errors and Next are the two non-task sections. Guide titles/slugs match
`areas.json` and features.json. A missing guide is allowed by the coverage check;
an existing guide must use feature IDs enabled for its brand. Keep guides within
250 to 600 words, seven H2s, and five steps per task. Notifications is at most
500 words; start is 200, API/page quickstarts are 300, Webhooks is 650, and Limits
uses tables with at most 100 words of surrounding prose. Code samples are code,
not prose. Tabs belong inside the step that differs; use `syncKey="way"`.

Brand selection happens before MDX parsing and search indexing. A page may set
`brands: [host]` or `brands: [hack]` in frontmatter; omit it for both. Within shared
pages use non-nested `{/* brand:host */}` or `{/* brand:hack */}` blocks, closed
with `{/* /brand */}`. `@PRODUCT@`, `@DOMAIN@`, `@INSTALL@`, and `@LLMS@` expand for
the selected brand. `.generated-content` and `.astro` are disposable per build.
Use `<Instance kind="hosted">` / `<Instance kind="self">` for runtime instance
variants. Both variants are excluded from Pagefind so search covers only shared instructions. Preserve limitstext.go's exact
phrases, including spaces, so the server can substitute configured limits.

Self-hosting and Enterprise, and the changelog, are not hand-written MDX: `scripts/docs-repo-pages.py`
renders them from `docs/configuration.md`, `docs/advanced/*.md`, `docs/platforms/*.md`,
`docs/operations/*.md`, `docs/kubernetes.md` and `CHANGELOG.md` into plain `.md` under
`.generated-content` during `prepare()`, rewriting relative links to the matching `/docs/` page or
a GitHub link, and (Host brand only) dropping anything that mentions Simple Hack. The page list it
writes to `self-hosting-pages.json` feeds the sidebar in `astro.config.mjs` and the section home
page. The content loader's glob covers both `.mdx` and `.md` for this reason.

Every built page is also served as `/docs/<page>.md`, for agents: `write_md_copies()` converts the
brand-selected source (Starlight components to Markdown/links, `<Instance>` to the same
`<!--sh:hosted-->`/`<!--sh:self-->` markers the HTML uses) during `externalize()`; the Go handler
runs the same runtime text passes on it as the HTML, minus chrome and scripts.

Legacy fragments are mapped per brand in `old-anchors.json`. The build writes each
map into `old-anchors.js`; Go tests verify every target page and heading in the
embedded brand tree. `install-skills` goes to `/install.html` on both brands.
`address-families` goes to the Host addresses page without a fragment, because
its hosted-only heading is absent on self-hosted installs. A handler test checks
this target against the served, runtime-filtered self-hosted page.

## Shell and scripts

Starlight supplies sidebar, search, TOC, Steps, synced Tabs, and previous/next.
Header is a docs search bar; PageFrame adds the shared product header/footer
markers, and Head emits the head marker before page styles. ThemeProvider and
ThemeSelect are empty: only the product's theme partial decides `data-theme`.
The stylesheet uses site.css tokens. No fonts are downloaded by the docs build.

The post-build step extracts all inline scripts, including Starlight's synced-tab
restore script, into deterministic `_scripts/<hash>.js` files without changing
script order or attributes. The Go handler similarly extracts the shared chrome's
scripts from its assembled HTML to `/docs/_chrome/<hash>.js`. These are the exact
shared scripts, not copies or a second theme implementation. Both paths are
checked for inline scripts. The shared UI CSP permits `wasm-unsafe-eval` only
under `/docs/`, for Pagefind’s local WebAssembly search, and never JavaScript eval. Pagefind's index is built separately for each brand.
