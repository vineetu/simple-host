# Image matching (checks against known child sexual abuse material)

## What it does

Every image written to a site is checked after the write against the NCMEC and IWF hash sets of
known child sexual abuse material (CSAM) through Microsoft PhotoDNA Cloud Service. Covered
writes: site deploys (`PUT /v1/sites/...`, the connector's `create_site` and `update_site`, the
photos page and upload links, WordPress and Squarespace imports), files-storage objects
(`storage_put_file` and the REST PUT), and on Simple Hack every event icon and entry screenshot.
The request never waits. The image is queued, its SHA-256 is checked against a local table of
earlier verdicts, and an unknown image is sent as bytes (WebP re-encoded as PNG, large images
scaled to 1600 px). Raster images only; SVG, icons, and images under 50 px are skipped.
Microsoft keeps no copy. Nothing about the visitor or owner goes with the image, never an IP
address. The service's answer and a tracking id are recorded.

The whole feature is off until `PHOTODNA_API_KEY` or `PHOTODNA_API_KEY_FILE` is set. Without the
key nothing is queued and a deploy changes nothing.

## Turning it on

1. Apply for PhotoDNA Cloud Service at https://myphotodna.microsoftmoderator.com/Profile. It is
   free for qualifying organisations. Microsoft issues a subscription key once approved.
2. Keep the key in a root-only file and hand it to the service as a systemd credential. On the
   hosted box: `sudo install -m 600 -o root -g root /dev/stdin /etc/simple-host-secrets/photodna-key`
   and paste the key, then in a drop-in for the unit add
   `LoadCredential=photodna-key:/etc/simple-host-secrets/photodna-key` and
   `Environment=PHOTODNA_API_KEY_FILE=%d/photodna-key`. `simple-hack.service` takes the same
   pattern in its own drop-in. The env files are `/etc/simple-host.env` and `/etc/simple-hack.env`;
   the other knobs go there.
3. Set `IMAGE_SCAN_REPORT_ORG` and `IMAGE_SCAN_REPORT_NAME`. A report is refused without the
   organisation name. Set `IMAGE_SCAN_ALERT_EMAIL` if alerts should go somewhere other than the
   support contact.
4. If `IMAGE_SCAN_EVIDENCE_DIR` points outside `DATA_DIR`, add it to `ReadWritePaths=` in the
   unit, or the hardened service cannot write there.
5. `sudo systemctl restart simple-host` (and `simple-hack`).

Small box (Docker Compose): set the same names in `/opt/simple-host/.env`, then `docker compose up -d`.

## Verifying it works

- The admin page's Moderation tab, Image matches, says "on" and names the provider, with queue counts.
- `GET /v1/admin/image-scan` with the admin key answers `enabled: true`.
- The startup log has the line `image matching on`. An unreadable key file logs a warning and
  leaves matching off.
- Deploy a site with an ordinary photo: the queue count rises and falls, and no match appears.

## What happens on a match

In this order, with no human in the loop:

1. A row in `image_scan_matches`: site, owner handle and email, path, version, the writer
   reference for a visitor upload, provider, tracking id, hash set source, violation codes, and
   the service's own answer.
2. A copy of the original file and a `record.json` in `IMAGE_SCAN_EVIDENCE_DIR/<match id>/`
   (mode 0700, never served, kept until you remove it; a record outlives a deleted site or account).
3. The file is removed from every kept version and the live copy; a storage object is deleted;
   an event icon or entry screenshot is cleared.
4. The site is taken down with the existing operator take-down. Nothing else is deleted; the
   owner sees "Taken down by the operator after an automated content check". On Simple Hack the
   event is taken down.
5. An email to `IMAGE_SCAN_ALERT_EMAIL` names the match and what to do. It never carries the image.
6. With `PHOTODNA_AUTO_REPORT=on` and `IMAGE_SCAN_REPORT_ORG` set, the CyberTipline report is
   filed at once. Otherwise the match waits for the Report button.

A known matching hash seen again is quarantined at once without a service call. Service errors
retry with backoff (1 min, doubling to 6 h, 12 tries).

## Filing the report

18 U.S.C. 2258A requires a provider that learns of apparent child sexual abuse material to
report it to NCMEC's CyberTipline, and to preserve the material and the report so that law
enforcement can obtain them (one year since the REPORT Act of 2024). The evidence folder is the
preservation; the report is yours to file.

- Automatic: `PHOTODNA_AUTO_REPORT=on`. Every match is reported through PhotoDNA's Report
  operation as soon as it is recorded, with the organisation, reporter name, and email from the knobs.
- By hand: on the admin Moderation tab, Image matches, each match has a Report button
  (`POST /v1/admin/image-scan/{id}/report`). 409 means it is already filed; 503 means no reporter
  is configured (`IMAGE_SCAN_REPORT_ORG` is empty).
- If PhotoDNA's Report operation is unavailable, file directly at https://report.cybertip.org
  from the evidence folder, and note the report id in `record.json`.

Do not open, copy, or forward the quarantined image beyond what the report needs. Do not restore the site before the report is filed.

## The evidence folder

`IMAGE_SCAN_EVIDENCE_DIR` (default `<DATA_DIR>/.evidence`, mode 0700), one folder per match:

```
<match id>/
  record.json       the image_scan_matches row, the service's answer, and the report id once filed
  <original name>   the file as uploaded
```

It is under no served path. Remove a folder only after the preservation period has passed and
the report is on record.

## What is not covered

- Images already on disk before the key was set are not scanned. There is no back-scan.
- Video is not checked.
- Text and documents are out of scope.
- The check runs after the write, so a matched file can be served for the moments between the
  write and the verdict.

## Knobs

| Knob | Default | Meaning |
|---|---|---|
| `PHOTODNA_API_KEY` | none | Subscription key for Microsoft PhotoDNA Cloud Service. Secret. Empty: image matching is off and nothing is queued. Prefer `PHOTODNA_API_KEY_FILE`. |
| `PHOTODNA_API_KEY_FILE` | none | A file holding the key, read once at start when `PHOTODNA_API_KEY` is empty, e.g. a systemd credential (`LoadCredential=photodna-key:/etc/simple-host-secrets/photodna-key` and `Environment=PHOTODNA_API_KEY_FILE=%d/photodna-key`). Unreadable: matching is off, with a warning in the log. |
| `PHOTODNA_MATCH_URL` | `https://api.microsoftmoderator.com/photodna/v1.0/Match` | The Match operation. |
| `PHOTODNA_REPORT_URL` | `https://api.microsoftmoderator.com/photodna/v1.0/Report` | The Report operation, which files a CyberTipline report with NCMEC through PhotoDNA. |
| `PHOTODNA_AUTO_REPORT` | `off` | `on`: every match is reported through the Report operation as soon as it is recorded. `off`: the admin Moderation tab offers a Report button per match. |
| `IMAGE_SCAN_REPORT_ORG` | none | Organisation name on reports. A report is refused without it. |
| `IMAGE_SCAN_REPORT_NAME` | none | Reporter's name on reports. |
| `IMAGE_SCAN_REPORT_EMAIL` | the support contact | Reporter's email on reports. |
| `IMAGE_SCAN_ALERT_EMAIL` | the support contact | Where each match alert is emailed. |
| `IMAGE_SCAN_EVIDENCE_DIR` | `<DATA_DIR>/.evidence` | Folder for the quarantined copy and the record of each match (mode 0700, under no served path). Must be writable by the service (`ReadWritePaths` on a hardened unit). |
