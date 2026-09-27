-- Saved data, step 1, review round 2 (2026-09-27): a running size of each
-- site's history, so a write past SAVED_DATA_HISTORY_MAX_MB is thinned at
-- once. Runs after sd1-saved-data-safety2-limits.sql (lexical order).
-- Idempotent; one transaction with a short lock timeout (retry if it gives up).
BEGIN;
SET LOCAL lock_timeout = '5s';

-- What a site's history holds (every earlier value and diff), kept by the
-- trigger below in the same transaction as every history write, so a write
-- that pushes it past SAVED_DATA_HISTORY_MAX_MB thins that site's history
-- right away instead of waiting for the sweep. A row's size is the text of
-- its value (prev) or diff, the same measure the thinning uses.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS history_bytes BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION sh_history_bytes() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    UPDATE sites s SET history_bytes = s.history_bytes + d.n
      FROM (SELECT site_id, sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0)) AS n
              FROM sh_hnew GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  ELSIF TG_OP = 'DELETE' THEN
    UPDATE sites s SET history_bytes = s.history_bytes - d.n
      FROM (SELECT site_id, sum(COALESCE(octet_length(prev::text), octet_length(diff::text), 0)) AS n
              FROM sh_hold GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  ELSE
    UPDATE sites s SET history_bytes = s.history_bytes + d.n
      FROM (SELECT site_id, sum(n) AS n FROM (
              SELECT site_id, COALESCE(octet_length(prev::text), octet_length(diff::text), 0) AS n FROM sh_hnew
              UNION ALL
              SELECT site_id, -COALESCE(octet_length(prev::text), octet_length(diff::text), 0) FROM sh_hold) x
             GROUP BY site_id) d
     WHERE s.id = d.site_id AND d.n <> 0;
  END IF;
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS data_history_bytes_ins ON data_history;
CREATE TRIGGER data_history_bytes_ins AFTER INSERT ON data_history
  REFERENCING NEW TABLE AS sh_hnew FOR EACH STATEMENT EXECUTE FUNCTION sh_history_bytes();
DROP TRIGGER IF EXISTS data_history_bytes_upd ON data_history;
CREATE TRIGGER data_history_bytes_upd AFTER UPDATE ON data_history
  REFERENCING OLD TABLE AS sh_hold NEW TABLE AS sh_hnew FOR EACH STATEMENT EXECUTE FUNCTION sh_history_bytes();
DROP TRIGGER IF EXISTS data_history_bytes_del ON data_history;
CREATE TRIGGER data_history_bytes_del AFTER DELETE ON data_history
  REFERENCING OLD TABLE AS sh_hold FOR EACH STATEMENT EXECUTE FUNCTION sh_history_bytes();

-- Backfill (a no-op on a new database). The triggers above lock data_history
-- until the end of the transaction, so no history write slips in between.
UPDATE sites s SET history_bytes = COALESCE((
  SELECT sum(COALESCE(octet_length(h.prev::text), octet_length(h.diff::text), 0))
    FROM data_history h WHERE h.site_id = s.id), 0);

COMMIT;
