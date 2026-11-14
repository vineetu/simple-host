-- Run with BOTH services stopped, before starting the new reader for the first
-- time, or after an older program wrote only the column. Never use a stale column
-- after D3-S5b: explicitly confirm it is authoritative with -v column_is_newer=yes.
\set ON_ERROR_STOP on
\if :{?column_is_newer}
SELECT :'column_is_newer' = 'yes' AS column_confirmed \gset
\else
\set column_confirmed false
\endif
\if :column_confirmed
BEGIN;
DELETE FROM hack_grant_teams h USING oauth_grants g
WHERE h.grant_id = g.id AND h.team_id IS DISTINCT FROM g.selected_team_id;
INSERT INTO hack_grant_teams (grant_id, team_id)
SELECT id, selected_team_id FROM oauth_grants WHERE selected_team_id IS NOT NULL
ON CONFLICT (grant_id) DO NOTHING;
COMMIT;
\else
DO $$ BEGIN
  RAISE EXCEPTION 'Refused: pass -v column_is_newer=yes only while the column is authoritative and both services are stopped.';
END $$;
\endif
