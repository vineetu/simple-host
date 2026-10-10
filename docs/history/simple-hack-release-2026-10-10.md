# Simple Hack standalone v0.8.7

Status: published, verified, and pinned on 2026-10-10.

The owner approved a standalone release matching main's click-to-play narrated
landing film. The latest previous Hack tag was hack-v0.8.6. Annotated tag
hack-v0.8.7 points at 6fd7a6418f3e9d2fcbffe63022a0566cc0349934, the current
origin/main when tagged. Its message is "Simple Hack v0.8.7: landing film plays
on click, with narration". The workflow checks out the tag and builds both
images from that checkout. [Workflow 38082528212](https://github.com/vineetu/simple-host/actions/runs/38082528212)
passed and published the [release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.7).

Anonymous ZIP and tar.gz downloads match SHA256SUMS and all 31 packaged files
match each other. The package defaults name 0.8.7, and README.txt records the
tagged source commit. Both ghcr.io/vineetu/simple-host:hack-0.8.7 and
ghcr.io/vineetu/simple-hack:0.8.7 pull anonymously for linux/arm64 and
linux/amd64. Extracted binaries have the expected ELF architecture, match
between base and Hack images, and execute natively on arm64 and through a
locally extracted QEMU on amd64: v0.8.7, commit 6fd7a64. The missing local
`file` utility was replaced with direct ELF header checks; no system package
was installed. Repository and published-package install_test.sh checks pass.

The published 0.8.7 package passed a fresh smoke installation with 55 recorded
migrations. A real published 0.8.6 package/image installation upgraded to the
published 0.8.7 image, applying three migrations from 52 to 55. The fixture
retained its event, membership, entry, team key, site files, domain, settings,
credentials, and local TLS CA across restart and upgrade. Both runs passed
TEST_HACK_PRESENTATION=1. The landing page contains eight `class="cap" data-i=`
scenes and the play-with-sound control, names the instance's own host instead
of simple-hack.app, and serves its versioned narration URL as 200 audio/mpeg
and 206 with the expected Content-Range for bytes 0-1023.

Tests used loopback ports 18474 through 18476, synthetic accounts, a fake mail
key, and a local Caddy CA. Smoke commands, including fixture Docker builds,
ran under MemoryMax=3G and MemorySwapMax=0. All test containers, volumes,
networks, fixture images, and temporary fixture data were removed. Installer
pins changed only after these checks passed. check-docs-sync.sh passed.
Production binaries, services, nginx, and the simplehost Postgres role were
untouched. No new browser matrix, Coolify/cloud deployment, external email,
or public ACME test was run; earlier evidence keeps its original release date.
