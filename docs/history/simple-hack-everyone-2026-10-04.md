# Hackathons, now for everyone

Status: shipped; production and standalone v0.8.5 verified, 2026-10-04.

The owner’s October 4 decision keeps the word hackathon and opens the audience to
non-programmers, PMs and leaders as well as college students who know tech. The
organiser chooses the platform because participants describe their ideas and AI
builds their team sites. “The best idea wins” is positioning; judging still uses
the organiser’s rubric.

The film stays at 12 scenes. Scenes 0, 3, 8, 9 and 10 have the approved copy;
scene 8 uses the existing rough.js chat, laptop, notes, arrow and creatures to
show “match volunteers to shifts” becoming a live shift-matching site. The old
judges art moves to 9; 10 keeps its old art. The separate Free scene is removed,
and Free appears in the opening and final subs. Camera framing and transition
dips follow the new sequence; the day/night tear remains at 5. Return-visit
headline, title and description match the new positioning.

“No coding needed” appears on participant join and event screens, the built-in
public page by its signup status, and Get started beside its participant build
prompt. Judge/co-organiser joins do not show participant building guidance.

INTENT, FEATURES, CHANGELOG, PARITY, README, active design docs, self-host guides,
Hack llms text and the Host hackathons page carry the updated story. Hack skill
text is 0.27.20, toolkit/listings 0.2.8. The participant skill tells the agent to
build for someone who may not code. Earlier immutable ZIPs keep their bytes;
no marketplace publication or plugin submission occurred. Toolkit version 13
is live, including the current reader and four new ZIPs.

Browser evidence: all 12 scenes photographed and visually inspected at 390×844,
320×568 and 1280×900. Every scene was walked by tap, swipe and keyboard; skip,
return visits, replay, back and wheel also passed. Two caption spacing fixes
apply only to the changed opening/final scenes on short phones. The scene-8
note was moved above its bubble after desktop review found it touching the
headline; all three widths then passed artwork/caption separation checks.
The joining, participant and public screens pass at all three widths, and the
participant example copies correctly. Toolkit page/reader/download checks pass
locally and live at all three widths.

Assumptions: the supplied film text is exact; “keep” means retain existing art
and navigation, including the old final illustration. The public built-in page
asks people to obtain the organiser’s join link, so its guidance sits by signup
status. No event rules, scoring logic, permissions or backend behavior changed.


Production: feature commit `572a2f9` was fast-forward pushed under the deploy
lock, built with version/commit ldflags under a 2 GB memory cap, backed up and
installed. Both services and neighbouring sites passed client checks. Simple
Host home HTML (nonce normalised) and phone/desktop screenshots were unchanged.
The final pin/documentation commit uses the same locked deploy procedure; five
binary backups remain. Full `make check`, including docs-sync and disposable
PostgreSQL tests, passes. No production schema or role changes were needed.

Live at simple-hack.app: all 12 scenes pass at 390 px using taps, actual touch
swipes and keys, then skip, return and replay. The twelve live phone shots were
visually reviewed and saved to the owner's requested everyone-shots directory.
Live Get started passes six viewport/theme states, copied prompts and native
accordions without JavaScript.

Standalone `hack-v0.8.5` (`572a2f9`) passed public download checksums and ZIP/tar
byte parity, anonymous pulls of both images on arm64 and amd64, native arm64
version/runtime checks, and amd64 ELF/commit checks. Published-package fresh
installation and an actual published 0.8.4 package/image upgrade to 0.8.5 retain
events, members, entries, team keys/sites, settings, credentials and TLS CA,
with 50 migrations. Both runs pass own-domain presentation, 42 browser states
and local Coolify routing through real Traefik. Installer pins moved only after
verification. The amd64 runtime and external cloud deployments were not repeated.
Disposable test containers and their volumes were removed.
