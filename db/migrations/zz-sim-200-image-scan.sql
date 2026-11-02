-- Image matching (SIM-200, 2026-11-02). Uploaded images are checked against
-- known abuse hash sets through PhotoDNA. image_scan_hashes remembers each
-- verdict by SHA-256 so the same bytes are never sent twice;
-- image_scan_queue holds work for the background worker; image_scan_matches
-- is the record of each match (no foreign keys on purpose: a record must
-- outlive a deleted site, event or account). Nothing here holds an image.
CREATE TABLE IF NOT EXISTS image_scan_hashes (
  sha256 TEXT PRIMARY KEY,
  verdict TEXT NOT NULL CHECK (verdict IN ('clean','match','skipped')),
  provider TEXT NOT NULL DEFAULT '',
  tracking_id TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  checked_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS image_scan_queue (
  id BIGSERIAL PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('site_file','storage_file','event_icon','entry_screenshot')),
  site_id UUID, event_id UUID, team_id UUID, version INTEGER,
  resource_name TEXT NOT NULL DEFAULT '', path TEXT NOT NULL DEFAULT '',
  writer_id TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL, bytes BIGINT NOT NULL DEFAULT 0,
  attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS image_scan_queue_next ON image_scan_queue (next_attempt_at);
CREATE TABLE IF NOT EXISTS image_scan_matches (
  id BIGSERIAL PRIMARY KEY,
  kind TEXT NOT NULL, site_id UUID, site_name TEXT NOT NULL DEFAULT '', owner_id UUID, owner_handle TEXT NOT NULL DEFAULT '', owner_email TEXT NOT NULL DEFAULT '',
  event_id UUID, event_slug TEXT NOT NULL DEFAULT '', team_id UUID, version INTEGER,
  resource_name TEXT NOT NULL DEFAULT '', path TEXT NOT NULL DEFAULT '', writer_id TEXT NOT NULL DEFAULT '',
  sha256 TEXT NOT NULL, bytes BIGINT NOT NULL DEFAULT 0, content_type TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '', tracking_id TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT '', violations TEXT NOT NULL DEFAULT '',
  raw JSONB, evidence_path TEXT NOT NULL DEFAULT '', uploaded_at TIMESTAMPTZ, detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  taken_down BOOLEAN NOT NULL DEFAULT false, alerted_at TIMESTAMPTZ,
  report_status TEXT NOT NULL DEFAULT 'pending' CHECK (report_status IN ('pending','filed','failed','off')),
  report_id TEXT NOT NULL DEFAULT '', reported_at TIMESTAMPTZ, report_error TEXT NOT NULL DEFAULT '',
  uploader TEXT NOT NULL DEFAULT '', address TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS image_scan_matches_detected ON image_scan_matches (detected_at DESC);
