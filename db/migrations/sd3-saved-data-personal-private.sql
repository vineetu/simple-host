-- Saved data, step 3 review (2026-09-27): a Personal name (kind mine) is
-- stored private = true. Nothing that knows the kinds reads the flag for it;
-- code that does not (a binary from before v0.7.0, after a rollback) then
-- treats the name as owner-only instead of public. The server writes it so on
-- every declaration from now on; this brings any name declared before that
-- into line. Runs after sd3-saved-data-personal-board.sql (lexical order).
-- Idempotent; touches only Personal rows (none exist before v0.7.0).
BEGIN;
SET LOCAL lock_timeout = '5s';

UPDATE collection_settings SET private = true, updated_at = now()
 WHERE kind = 'mine' AND NOT private;

COMMIT;
