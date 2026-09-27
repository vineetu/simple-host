-- Saved data, step 2: kinds (saved-data redesign, approved 2026-09-27).
-- Every data name on a site can be declared as one kind: Page info
-- (content: the owner writes, everyone reads) or Submissions (entries:
-- visitors send, the owner sees all, each visitor sees and changes their own;
-- private to the owner unless made public). The owner chooses who may save
-- (anyone who signs in, or only listed emails and domains, plus a block list)
-- and whether new submissions are emailed. Runs after the sd1-* files
-- (lexical order). Idempotent and additive; ADD COLUMNs with constant
-- defaults are metadata-only.
--
-- Sites that exist before this step keep today's behaviour: sites.legacy_data
-- (added true for every site by sd1) stays true for them. The server creates
-- every new site with legacy_data = false, where a name nobody declared takes
-- no saves (declare_first). The column default is left true on purpose, so a
-- site created by an older binary during the deploy is never made strict.
BEGIN;
SET LOCAL lock_timeout = '5s';

-- A declared name: its kind (NULL = not declared; a legacy site's list then
-- behaves as before), one entry per person, and the owner's email choice
-- (off, each = batched every SAVED_DATA_NOTIFY_EACH_MINUTES, daily). The
-- existing private column is the Submissions visibility (true = owner only).
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS kind TEXT;
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS one_per_person BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS notify TEXT NOT NULL DEFAULT 'off';
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS notify_sent_at TIMESTAMPTZ;
ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS declared_at TIMESTAMPTZ;

-- Who may save on a site: 'anyone' (anyone who signs in, the default) or
-- 'listed' (only the emails and @domains in site_savers' allow list). The
-- block list applies in both modes.
ALTER TABLE sites ADD COLUMN IF NOT EXISTS savers_mode TEXT NOT NULL DEFAULT 'anyone';

CREATE TABLE IF NOT EXISTS site_savers (
  site_id  UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  list     TEXT NOT NULL CHECK (list IN ('allow', 'block')),
  pattern  TEXT NOT NULL,
  added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (site_id, list, pattern)
);

-- A visitor's own entries, newest first.
CREATE INDEX IF NOT EXISTS idx_collection_items_mine
  ON collection_items (site_id, collection, submitted_by, id DESC) WHERE submitted_by IS NOT NULL;

COMMIT;
