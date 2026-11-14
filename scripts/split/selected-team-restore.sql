-- Run with BOTH services stopped before installing a pre-D2-S6 program after
-- D3-S5b. This removes only the selected-team reader requirement; the shared
-- oldest-safe-commit floor must also permit the older program (brief section 1.3).
-- The table wins, including absent selections; keep it for a later reconcile.
\set ON_ERROR_STOP on
BEGIN;
UPDATE oauth_grants g
SET selected_team_id = (SELECT h.team_id FROM hack_grant_teams h WHERE h.grant_id = g.id)
WHERE g.selected_team_id IS DISTINCT FROM
      (SELECT h.team_id FROM hack_grant_teams h WHERE h.grant_id = g.id);
COMMIT;
