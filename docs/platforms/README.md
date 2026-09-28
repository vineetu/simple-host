# Where it runs

The small box needs three things: a process that is always on, a disk that
keeps its files, and Postgres. Anywhere that gives all three can run it.

| Where | How | Status |
|---|---|---|
| Any Ubuntu VPS, including UpCloud | the installer, [simple-host.app/setup](https://simple-host.app/setup) | tested |
| Hostinger VPS | the installer | an Ubuntu VPS like any other |
| Fly.io | [this guide](fly.md) | tested 2026-09-28, about $4.50/month |
| Render | [this guide](render.md) | about $13.55/month, needs a card. Tested 2026-09-28 on the free plan (TLS, publish, visitor IP, analytics); the disk and the migration step are paid and not yet run |
| Railway | | not yet tested |
| Vercel, Netlify, shared PHP hosting | | not as-is: a 4.5 MB request limit (smaller than a site upload), no disk that keeps files, no always-on process |
