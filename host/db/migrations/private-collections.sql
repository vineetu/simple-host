-- Private collections (owner decision 2026-09-24): a collection can be marked
-- private by the site owner. Only a signed-in visitor on the site's own domain
-- can submit to it, each item is stamped server-side with who sent it, and only
-- the owner (and the platform admin) can read it; a submitter cannot read items back.
--
-- Apply once to an existing database, as the table owner, BEFORE deploying the
-- build that reads it (the server refuses to start without these columns):
--   sudo -u postgres psql -d simplehost -f db/migrations/private-collections.sql
-- Idempotent.

-- One row per collection that has a setting. No row = public (the default), so
-- every existing collection is unchanged.
CREATE TABLE IF NOT EXISTS collection_settings (
  site_id    UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  collection TEXT NOT NULL,
  private    BOOLEAN NOT NULL DEFAULT false,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, collection)
);

-- The account that submitted an item to a private collection. Set by the
-- server from the visitor session, never from the request body; NULL for every
-- item written any other way (public collections, items from before).
ALTER TABLE collection_items
  ADD COLUMN IF NOT EXISTS submitted_by UUID REFERENCES users(id) ON DELETE SET NULL;

-- Tables created as postgres are invisible to the service role until granted
-- (the visitor-grants.sql lesson). Skipped where that role does not exist.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'simplehost') THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE collection_settings TO simplehost;
  END IF;
END $$;
