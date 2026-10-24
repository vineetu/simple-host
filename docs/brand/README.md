# Brand sources

Simple Host's mark is "Rounded" (approved 2026-10-24): a bold hand-drawn `s.h` monogram with a
teal period, ink on a cream tile. Letters start from Nunito (SIL OFL, `Nunito-OFL.txt`), then
customised. Background: `#f6f4e9` and `#103f49`, dot: teal `#157e8a` (cream tile) or `#3fb0bb`
(ink tile). Simple Hack keeps its own mark (`b_hack.svg`, `favicon_hack.svg`), unchanged.

- `rounded-512.svg` — the 512 px master, cream tile. Source for `/favicon.svg`, the
  apple-touch-icon render, host-toolkit downloads, the OpenAI plugin icon/logo, and the Claude
  plugin icon.
- `rounded-16.svg` — a hand-placed 16 px pixel master (`shape-rendering="crispEdges"`), used to
  render the 16 px favicon layer crisply instead of scaling the 512 master down.
- `rounded-dark.svg`, `rounded-mono.svg` — dark-page and single-color variants.
- `reversed-512.svg`, `reversed-16.svg`, `reversed-dark.svg`, `reversed-mono.svg` — the same
  mark on an ink tile with cream letters, for light pages.

Raster exports (favicon.ico layers, favicon-32.png, apple-touch-icon.png, the host-toolkit
downloads, the OpenAI and Claude plugin icons) are flattened to opaque cream at the corners —
none of them carry transparency, matching every icon asset this project has shipped before.
`/favicon.svg` alone keeps the mark's natural rounded-corner tile with transparent corners,
since it is vector and browsers composite it against the tab's own background.

`old/` holds the prior mark (`b_host.svg`, `favicon_host.svg`), kept for reference.

Full design process, finalist comparisons and the rejected concepts: see the report at
`jobs/shlogo2.report.md` and `jobs/shlogo2/final/` in the orchestrator workspace (not part of
this repo).
