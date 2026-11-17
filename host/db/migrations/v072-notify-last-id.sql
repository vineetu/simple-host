-- Submission emails count entries by id, not by time (v0.7.2, 2026-09-27,
-- correctness review L3).
--
-- The digest counted entries with created_at after the last email. An entry
-- whose transaction began before a digest ran but committed after it was
-- never counted by any later digest either (its created_at is the
-- transaction's start). Entries of a Submissions name are added one at a time
-- under the name's lock, so their ids are in commit order: the digest now
-- remembers the last id it counted and counts ids after it. NULL (every name
-- today) keeps the time rule once, until the next email records an id.
--
-- Additive and idempotent: one nullable column, no rewrite. Apply before
-- deploying the build that reads it (the server refuses to start without it).

ALTER TABLE collection_settings ADD COLUMN IF NOT EXISTS notify_last_id BIGINT;
