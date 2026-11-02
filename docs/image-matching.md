# Image matching (checks against known child sexual abuse material)

## What it does

Every image written to a site is checked after the write against hash sets of known child
sexual abuse material (CSAM) through Microsoft PhotoDNA Cloud Service. Covered writes: site
deploys (`PUT /v1/sites/...`, the connector's `create_site` and `update_site`, the photos page
and upload links, WordPress and Squarespace imports), files-storage objects (`storage_put_file`
and the REST PUT), and on Simple Hack every event icon and entry screenshot. The request never
waits. The image is queued, its SHA-256 is checked against a local table of earlier verdicts,
and an unknown image is sent as bytes (WebP re-encoded as PNG, large images scaled to 1600 px,
at most 4,000,000 bytes sent). A Match check sends only the image bytes, never a name, email,
or IP address. The service's answer and a tracking id are recorded.

JPEG, PNG, GIF, and WebP are checked; BMP, TIFF, SVG, and images under 160 pixels on the short
side (or that would drop under 160 when scaled) are skipped. Images already on disk before the
key was set are not scanned, and the check runs after the write, so a matched file can be
served for the moments between the write and the verdict.

The whole feature is off until `PHOTODNA_API_KEY` or `PHOTODNA_API_KEY_FILE` is set. Without the
key nothing is queued, nothing is offered to users, and a deploy changes nothing.

## Turning it on

The settings are for a server install (a systemd unit). Docker Compose small boxes do not pass
these settings through yet.

1. Apply for PhotoDNA Cloud Service at https://myphotodna.microsoftmoderator.com/Profile. It is
   free for qualifying organisations. Microsoft issues a subscription key once approved.
2. Keep the key in a root-only file and hand it to the service as a systemd credential. On the
   hosted box: `sudo install -m 600 -o root -g root /dev/stdin /etc/simple-host-secrets/photodna-key`
   and paste the key, then in a drop-in for the unit add
   `LoadCredential=photodna-key:/etc/simple-host-secrets/photodna-key` and
   `Environment=PHOTODNA_API_KEY_FILE=%d/photodna-key`. `simple-hack.service` takes the same
   pattern in its own drop-in. The env files are `/etc/simple-host.env` and `/etc/simple-hack.env`;
   the other knobs go there.
3. Set `IMAGE_SCAN_REPORT_ORG`, `IMAGE_SCAN_REPORT_NAME`, and, if reports should not carry the
   support contact, `IMAGE_SCAN_REPORT_EMAIL`. Reports need all three (the email falls back to
   the support contact). Without them, matches are still quarantined but are recorded with
   report status "off". Set `IMAGE_SCAN_ALERT_EMAIL` if alerts should go somewhere other than
   the support contact.
4. If `IMAGE_SCAN_EVIDENCE_DIR` points outside `DATA_DIR`, add it to `ReadWritePaths=` in the
   unit, or the hardened service cannot write there. It must not be inside a served path or
   reach one through a symlink; the service refuses to write evidence there.
5. If you run your own backups, exclude the evidence folder (see below).
6. `sudo systemctl restart simple-host` (and `simple-hack`).

## Verifying it works

- The admin page's Moderation tab, Image matches, says "on" and names the provider, with queue counts.
- `GET /v1/admin/image-scan` with the admin key answers `enabled: true`, and `report_configured: true`
  once the organisation, reporter name, and report email are all set.
- The startup log has the line `image matching on`. An unreadable key file logs a warning and
  leaves matching off.
- Deploy a site with an ordinary photo: the queue count rises and falls, and no match appears.

## What happens on a match

In this order, with no human in the loop:

1. A row in `image_scan_matches`: site or event, owner handle and email, path, version, the
   writer reference for a visitor upload, provider, tracking id, hash set source, violation
   codes, and the service's own answer.
2. A copy of the original bytes and a `record.json` in `IMAGE_SCAN_EVIDENCE_DIR/<match id>/`
   (mode 0700, never served, kept until you remove it; a record outlives a deleted site or account).
3. The matched bytes are removed from every kept version and the live copy. Only copies whose
   SHA-256 equals the match are removed; a different file at the same path stays. A storage
   object is deleted only if it still holds the matched bytes; an event icon or entry screenshot
   is cleared only if it still holds them. If the evidence copy failed in step 2, this step is
   skipped and the file is kept in place, so the original is not lost.
4. The site is taken down with the existing operator take-down, under the same per-site lock as
   a take-down from the admin page. Nothing else is deleted; the owner sees "Taken down by the
   operator after an automated content check". On Simple Hack the event is taken down. This
   step runs even when the evidence copy failed.
5. With `PHOTODNA_AUTO_REPORT=on` and reporting configured (organisation, reporter name, and
   report email), the CyberTipline report is filed at once. Otherwise the match waits for the
   Report button.
6. An email to `IMAGE_SCAN_ALERT_EMAIL` names the match and lists each step with done or
   FAILED: evidence copy, file removal, take-down, and report filed, report failed, or report
   not filed. Unless the report was filed in this run, it carries the line "File the
   CyberTipline report (the Report button on the admin page, or https://report.cybertip.org/)
   as soon as reasonably possible; the law requires it." It never carries the image.

Any step that fails is listed as FAILED in the alert and the log; the operator acts on it. A
hash already on record is not sent again: a known match is quarantined at once, and a known
clean or skipped image is dropped from the queue. When the service fails, a job retries after
1 minute, then doubling, capped at 6 hours. After 12 failed tries it stops and shows as "gave
up" on the admin page's queue line, with the last error on the row. A site that is deleted and
then restored from the trash has its live images queued again.

## Filing the report

18 U.S.C. 2258A requires a provider that learns of apparent child sexual abuse material to
report it to NCMEC's CyberTipline, and to preserve the material and the report so that law
enforcement can obtain them (one year since the REPORT Act of 2024). The evidence folder is the
preservation; the report is yours to file.

A Report sends the image, the account handle and email of the person who uploaded it (for an
entry screenshot or event icon, the participant or organiser who uploaded it; for a site file,
the site owner), the site address or event, and the reporting organisation's contact details
to Microsoft's PhotoDNA Report service, which files it with NCMEC's CyberTipline.

- Reporting needs `IMAGE_SCAN_REPORT_ORG`, `IMAGE_SCAN_REPORT_NAME`, and a report email
  (`IMAGE_SCAN_REPORT_EMAIL`, or the support contact). A match recorded while any of them was
  missing has report status "off"; once all three are set, its Report button appears.
- Automatic: `PHOTODNA_AUTO_REPORT=on`. Every match is reported through PhotoDNA's Report
  operation as soon as it is recorded, before the alert is sent.
- By hand: on the admin Moderation tab, Image matches, each match with report status off,
  pending, or failed has a Report button (`POST /v1/admin/image-scan/{id}/report`). 409 means it
  is already filed; 503 means reporting is not configured; 502 means the report failed. A 502
  that says the report was filed with an id but saving failed means the report is on record
  with NCMEC: note that id, and do not file again.
- A report counts as filed only when the service acknowledges it with a report id. The list
  shows every match still waiting for a report (off, pending, or failed) plus the newest 200.
- If PhotoDNA's Report operation is unavailable, file directly at https://report.cybertip.org/
  from the evidence folder, and note the report id in `record.json`.

Do not open, copy, or forward the quarantined image beyond what the report needs. Do not restore the site before the report is filed.

## The evidence folder

`IMAGE_SCAN_EVIDENCE_DIR` (default `<DATA_DIR>/.evidence`, mode 0700), one folder per match:

```
<match id>/
  image.<ext>   the original bytes as uploaded
  record.json   the match record, the service's raw reply, the steps taken with done or FAILED,
                and the report status, id, and time once a report is attempted
```

It is under no served path. Remove a folder only after the preservation period has passed and
the report is on record.

Backups: on the hosted box the off-box backup excludes `.evidence`. If you run your own backups,
exclude the evidence folder. A matched file uploaded before a backup ran may already be in that
backup: check the backup for the path named in `record.json` and remove it there.

## Knobs

| Knob | Default | Meaning |
|---|---|---|
| `PHOTODNA_API_KEY` | none | Subscription key for Microsoft PhotoDNA Cloud Service. Secret. Empty: image matching is off and nothing is queued. Prefer `PHOTODNA_API_KEY_FILE`. |
| `PHOTODNA_API_KEY_FILE` | none | A file holding the key, read once at start when `PHOTODNA_API_KEY` is empty, e.g. a systemd credential (`LoadCredential=photodna-key:/etc/simple-host-secrets/photodna-key` and `Environment=PHOTODNA_API_KEY_FILE=%d/photodna-key`). Unreadable: matching is off, with a warning in the log. |
| `PHOTODNA_MATCH_URL` | `https://api.microsoftmoderator.com/photodna/v1.0/Match` | The Match operation. |
| `PHOTODNA_REPORT_URL` | `https://api.microsoftmoderator.com/photodna/v1.0/Report` | The Report operation, which files a CyberTipline report with NCMEC through PhotoDNA. |
| `PHOTODNA_AUTO_REPORT` | `off` | `on`: every match is reported through the Report operation as soon as it is recorded. `off`: the admin Moderation tab offers a Report button per match. |
| `IMAGE_SCAN_REPORT_ORG` | none | Organisation name on reports. Reports need it, `IMAGE_SCAN_REPORT_NAME`, and a report email; without all three, matches are recorded with report status off. |
| `IMAGE_SCAN_REPORT_NAME` | none | Reporter's name on reports. Required for reports. |
| `IMAGE_SCAN_REPORT_EMAIL` | the support contact | Reporter's email on reports. |
| `IMAGE_SCAN_ALERT_EMAIL` | the support contact | Where each match alert is emailed. |
| `IMAGE_SCAN_EVIDENCE_DIR` | `<DATA_DIR>/.evidence` | Folder for the quarantined copy and the record of each match (mode 0700, under no served path, symlinks resolved). Must be writable by the service (`ReadWritePaths` on a hardened unit). Exclude it from your own backups. |
