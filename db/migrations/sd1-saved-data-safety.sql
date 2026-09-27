-- Saved data, step 1: the safety floor (saved-data redesign, approved
-- 2026-09-27). Every change to a site's saved data and to a list item can be
-- undone for 30 days; deleting an item or clearing a list keeps the items for
-- 30 days; every write records who made it; a few counters measure how pages
-- use saved data before any rule is tightened.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without these columns):
--   sudo -u postgres psql -d simplehost -c 'SET ROLE simplehost' -f db/migrations/sd1-saved-data-safety.sql
-- Idempotent and additive. The ADD COLUMNs are metadata-only; the two indexes
-- on collection_items are built on a few hundred rows.

-- Every site that exists before the kinds arrive keeps today's open behaviour.
-- The default is true until the step that introduces kinds flips it.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS legacy_data BOOLEAN NOT NULL DEFAULT true;

-- Deleted and cleared items stay for the undo window (NULL = live), and the
-- address the author wrote from is kept beside the account id.
ALTER TABLE collection_items ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE collection_items ADD COLUMN IF NOT EXISTS submitted_email TEXT;
CREATE INDEX IF NOT EXISTS idx_collection_items_deleted
  ON collection_items (site_id, collection, deleted_at) WHERE deleted_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_collection_items_submitted_by
  ON collection_items (submitted_by) WHERE submitted_by IS NOT NULL;

-- One row per change: the value before it (prev), what changed, who and when.
-- kind 'state' is the site's saved-data document (name ''), kind 'list' one
-- item of a list (item_id). Kept 30 days by time, thinned past a per-site cap
-- but always keeping each item's first change of every day.
CREATE TABLE IF NOT EXISTS data_history (
  id          BIGSERIAL PRIMARY KEY,
  site_id     UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL CHECK (kind IN ('state', 'list')),
  name        TEXT NOT NULL DEFAULT '',
  item_id     BIGINT REFERENCES collection_items(id) ON DELETE CASCADE,
  op          TEXT NOT NULL,
  prev        JSONB,
  actor_id    UUID REFERENCES users(id) ON DELETE SET NULL,
  actor_kind  TEXT NOT NULL,
  actor_email TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_data_history_target ON data_history (site_id, kind, name, id DESC);
CREATE INDEX IF NOT EXISTS idx_data_history_item ON data_history (item_id) WHERE item_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_data_history_created ON data_history (created_at);
CREATE INDEX IF NOT EXISTS idx_data_history_actor ON data_history (actor_id) WHERE actor_id IS NOT NULL;

-- A write retried with the same Idempotency-Key is saved once: the first
-- answer's status is kept (24 hours) and replayed. scope is a hash of the key, the
-- route and the caller; status 0 = the first request is still running.
CREATE TABLE IF NOT EXISTS idempotency_keys (
  scope        BYTEA PRIMARY KEY,
  status       INTEGER NOT NULL DEFAULT 0,
  etag         TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_created ON idempotency_keys (created_at);

-- The 7-day watch: per site and day, how often each saved-data use that a
-- later step tightens happened (visitor whole-document replace, non-object
-- documents, visitor ops by type, large visitor increments, new list names,
-- large list items). Counts only, never content. Read by /v1/admin/data-watch.
CREATE TABLE IF NOT EXISTS data_watch (
  day     DATE NOT NULL,
  site_id UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  metric  TEXT NOT NULL,
  count   BIGINT NOT NULL DEFAULT 0,
  last_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (day, site_id, metric)
);
