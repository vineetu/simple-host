-- Step 2 of hash-api-keys.sql: drop the plaintext keys.
--
-- Run only after the binary that reads api_keys is live and verified: the
-- previous binary reads users.api_key and stops working once it is gone.
-- Re-copies first, so a key issued by the previous binary between step 1 and
-- the restart is not lost. APPLY AS THE ROLE IN DB_DSN. Safe to re-run.
BEGIN;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns
              WHERE table_schema = current_schema()
                AND table_name = 'users' AND column_name = 'api_key') THEN
    INSERT INTO api_keys (key_hash, user_id)
    SELECT encode(sha256(convert_to(api_key, 'UTF8')), 'hex'), id
      FROM users
     WHERE api_key IS NOT NULL AND api_key <> ''
    ON CONFLICT (key_hash) DO NOTHING;
    ALTER TABLE users DROP COLUMN api_key;
  END IF;
END $$;

COMMIT;
