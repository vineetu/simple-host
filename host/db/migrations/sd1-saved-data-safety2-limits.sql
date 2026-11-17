-- Saved data, step 1, review fixes (2026-09-27): a running size per site, PATCH
-- history as a diff, and idempotency answers that never keep a response body.
-- Runs after sd1-saved-data-safety.sql (lexical order). Idempotent; one
-- transaction; the triggers are created before the backfill and both lock the
-- two tables briefly, so no write slips between them.
BEGIN;
SET LOCAL lock_timeout = '5s';

-- What a site's live saved data takes: the page-data document (state_bytes)
-- plus every live list item (deleted items and history are not counted).
-- Kept by the triggers below in the same transaction as every write, so the
-- per-site cap (SAVED_DATA_SITE_MAX_MB) is one row read, never a scan.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS state_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN IF NOT EXISTS data_bytes BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION sh_sites_state_bytes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  s BIGINT := COALESCE(octet_length(NEW.state::text), 0);
BEGIN
  IF TG_OP = 'INSERT' THEN
    NEW.data_bytes := s;
  ELSE
    NEW.data_bytes := NEW.data_bytes + s - OLD.state_bytes;
  END IF;
  NEW.state_bytes := s;
  RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION sh_items_live_bytes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE sites s SET data_bytes = s.data_bytes + d.n
      FROM (SELECT site_id, sum(octet_length(data::text)) AS n FROM sh_new
             WHERE deleted_at IS NULL GROUP BY site_id) d
     WHERE s.id = d.site_id;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE sites s SET data_bytes = s.data_bytes - d.n
      FROM (SELECT site_id, sum(octet_length(data::text)) AS n FROM sh_old
             WHERE deleted_at IS NULL GROUP BY site_id) d
     WHERE s.id = d.site_id;
  ELSE
    UPDATE sites s SET data_bytes = s.data_bytes + d.n
      FROM (SELECT site_id, sum(n) AS n FROM (
              SELECT site_id, octet_length(data::text) AS n FROM sh_new WHERE deleted_at IS NULL
              UNION ALL
              SELECT site_id, -octet_length(data::text) FROM sh_old WHERE deleted_at IS NULL) x
             GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  END IF;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS sites_state_bytes ON sites;
CREATE TRIGGER sites_state_bytes BEFORE INSERT OR UPDATE OF state ON sites
  FOR EACH ROW EXECUTE FUNCTION sh_sites_state_bytes();
DROP TRIGGER IF EXISTS collection_items_bytes_ins ON collection_items;
CREATE TRIGGER collection_items_bytes_ins AFTER INSERT ON collection_items
  REFERENCING NEW TABLE AS sh_new FOR EACH STATEMENT EXECUTE FUNCTION sh_items_live_bytes();
DROP TRIGGER IF EXISTS collection_items_bytes_upd ON collection_items;
CREATE TRIGGER collection_items_bytes_upd AFTER UPDATE ON collection_items
  REFERENCING OLD TABLE AS sh_old NEW TABLE AS sh_new FOR EACH STATEMENT EXECUTE FUNCTION sh_items_live_bytes();
DROP TRIGGER IF EXISTS collection_items_bytes_del ON collection_items;
CREATE TRIGGER collection_items_bytes_del AFTER DELETE ON collection_items
  REFERENCING OLD TABLE AS sh_old FOR EACH STATEMENT EXECUTE FUNCTION sh_items_live_bytes();

-- Backfill (a no-op on a new database). The UPDATE does not name state, so
-- the state trigger does not fire.
UPDATE sites s SET
  state_bytes = COALESCE(octet_length(s.state::text), 0),
  data_bytes  = COALESCE(octet_length(s.state::text), 0)
              + COALESCE((SELECT sum(octet_length(ci.data::text)) FROM collection_items ci
                           WHERE ci.site_id = s.id AND ci.deleted_at IS NULL), 0);

-- A change made with PATCH ops keeps only what it changed (diff: how to turn
-- the document after it back into the one before), with a full copy (prev)
-- at least every SAVED_DATA_SNAPSHOT_EVERY changes and on each day's first.
ALTER TABLE data_history ADD COLUMN IF NOT EXISTS diff JSONB;

-- Idempotency-Key answers keep the status, the version or item id (ref) and
-- a hash of the request body (a reused key with another body is refused),
-- never the response body. site_id bounds the rows kept per site.
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS site_id UUID REFERENCES sites(id) ON DELETE CASCADE;
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS ref BIGINT;
ALTER TABLE idempotency_keys ADD COLUMN IF NOT EXISTS body_hash BYTEA;
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_site ON idempotency_keys (site_id, created_at);

COMMIT;
