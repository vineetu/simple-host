# Simple Hack standalone v0.8.8

Status: published, verified, and pinned on 2026-10-10.

The owner asked for the next standalone release once storage access presets
reached Simple Hack. The latest previous Hack tag was hack-v0.8.7. Annotated tag
hack-v0.8.8 points at dc9bfe4c8ce5c22c84522730adc6f4423f335e1a, the commit
deployed to simple-hack.app and verified live. Its message is "Simple Hack
v0.8.8: storage access presets". A first push of the tag pointed at a later
origin/main that carried three unrelated commits from another session; that
workflow run was cancelled before it published anything (no release, no images),
and the tag was moved to dc9bfe4 before any download existed. The workflow then
checked out the tag, built both images from that checkout, and published the
[release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.8).

Anonymous ZIP and tar.gz downloads match SHA256SUMS, and all 31 packaged files
match each other. The package defaults name 0.8.8, and README.txt records source
dc9bfe4. Both ghcr.io/vineetu/simple-host:hack-0.8.8 and
ghcr.io/vineetu/simple-hack:0.8.8 pull anonymously for linux/arm64 and
linux/amd64. Extracted binaries have the expected ELF architecture and match
between the base and Hack images. The arm64 binary runs natively and reports
v0.8.8 at commit dc9bfe4 with 55 migrations. The amd64 binary carries the same
version and commit strings and the new code; it was not executed, as this box has
no amd64 emulation. Repository and published-package install_test.sh pass.

The published 0.8.8 package passed a fresh smoke installation with 55 recorded
migrations. A real published 0.8.7 package/image installation upgraded to the
published 0.8.8 image (no new migrations) and kept its event, membership, entry,
team key, site files, domain, settings, credentials, and local TLS CA across
restart and upgrade. Both runs passed TEST_HACK_PRESENTATION=1.

Tests used loopback ports 18474 through 18476, synthetic accounts, a fake mail
key, and a local Caddy CA, under MemoryMax=3G and MemorySwapMax=0. All test
containers, volumes, networks, and fixture images were removed. Installer pins
changed only after these checks passed.
