# Simple Hack standalone v0.8.9

Status: published, verified, and pinned on 2026-10-15.

Part of the owner's parity pass across every way Simple Host runs. The previous
Hack tag was hack-v0.8.8. Annotated tag hack-v0.8.9 points at d9d683a0, the
commit deployed to simple-hack.app and verified live. It carries everything since
0.8.8 that applies to Simple Hack: storage presets and the shared website tools
as Host has them (upload links, `keep_other_files`), the new red product icon,
judging-first Manage, unlisted team sites kept out of search engines, and a
self-hosted server that names itself: Get started shows a custom connector with
the server's own address for Claude (never the hosted directory listing), and
robots.txt and sitemap.xml name the server. The workflow built both images from
the tag and published the
[release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.9).

The ZIP and tar.gz match SHA256SUMS (45 files). `simple-hack:0.8.9` and
`simple-host:hack-0.8.9` are published for linux/arm64 and linux/amd64; the
arm64 binary reports v0.8.9 at commit d9d683a.

The published 0.8.9 package passed a fresh smoke installation (57 recorded
migrations). A real published 0.8.8 package and image installation (55
migrations) upgraded to the published 0.8.9 image, applied the two new
migrations, and kept its event, membership, team key, team site, domain,
settings, credentials and local TLS CA across restart and upgrade. Both runs
passed TEST_HACK_PRESENTATION=1 and an added check of the self-hosted Get
started (custom Claude connector with the server's own address), robots.txt and
the red icon (`jobs/parity2-evidence/hack-self-claude.mjs` on the owner's box).

Tests used loopback ports 18474 through 18476, synthetic accounts, a fake mail
key and a local Caddy CA, under MemoryMax=3G and MemorySwapMax=0. Test
containers, volumes and fixture images were removed. Installer pins changed only
after these checks passed.
