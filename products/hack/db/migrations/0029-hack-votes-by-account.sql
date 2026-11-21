-- Simple Hack people's-choice votes are kept per account (event_votes.voter_id)
-- instead of per base sign-in address (voter_email), one vote per (event, voter).
-- voter_email stays, nullable and unused.
ALTER TABLE event_votes ADD COLUMN IF NOT EXISTS voter_id UUID REFERENCES users(id) ON DELETE CASCADE;

-- Each vote goes to the account whose base address (lower-case, trimmed, any
-- +tag after the first character of the local part removed: host/model.BaseEmail)
-- is the voter_email; of several such accounts, the oldest.
UPDATE event_votes v SET voter_id = b.id
  FROM (
    SELECT DISTINCT ON (base) id, base FROM (
      SELECT id, created_at, regexp_replace(lower(btrim(username)), '^([^+]+)\+.*(@[^@]*)$', '\1\2') AS base
        FROM users
    ) x ORDER BY base, created_at, id
  ) b
 WHERE v.voter_id IS NULL AND v.voter_email = b.base;

-- A vote whose account is gone cannot be kept; Simple Hack data is disposable.
DELETE FROM event_votes WHERE voter_id IS NULL;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_constraint c
      JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
     WHERE c.conrelid = 'event_votes'::regclass AND c.conname = 'event_votes_pkey' AND a.attname = 'voter_email'
  ) THEN
    ALTER TABLE event_votes DROP CONSTRAINT event_votes_pkey;
  END IF;
  ALTER TABLE event_votes ALTER COLUMN voter_id SET NOT NULL;
  ALTER TABLE event_votes ALTER COLUMN voter_email DROP NOT NULL;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'event_votes'::regclass AND contype = 'p') THEN
    ALTER TABLE event_votes ADD PRIMARY KEY (event_id, voter_id);
  END IF;
END $$;
