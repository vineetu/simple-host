-- Saved data, steps 3 and 4 (saved-data redesign, approved 2026-09-27): two
-- more kinds a name can be declared as, both stored in collection_items and
-- collection_settings.kind like the others (no new table):
--   - mine (Personal): one private record per signed-in person per name,
--     keyed by the row's submitted_by. Only that person reads or writes it;
--     the site owner sees how many people have one and their total size.
--   - board (Shared board): a list anyone who can open the site reads and
--     signed-in visitors allowed to save add to, change and delete, one item
--     at a time; only the owner clears it.
-- version counts an item's changes so a board edit can say which version it
-- changed (If-Match, 409 version_conflict). Runs after sd2-* (lexical order).
-- Idempotent and additive: ADD COLUMN with a constant default is
-- metadata-only on PostgreSQL 11+, no table rewrite.
BEGIN;
SET LOCAL lock_timeout = '5s';

ALTER TABLE collection_items ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;

COMMIT;
