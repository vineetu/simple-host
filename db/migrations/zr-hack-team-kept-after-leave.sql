-- Leave this event, widened (SIM-340, owner decision 2026-10-31): past a team's
-- deadline, leaving never deletes a team it empties -- the team, its entry, its
-- site and its scores stay. kept_after_leave marks exactly that: a team LeaveMembership
-- kept instead of deleting because a participant leaving the event (never a team-only
-- move, and never an organiser's Remove person) emptied it while it was not
-- TeamKeepableSQL. ListTeamsWithMembers reads this flag instead of including every
-- memberless frozen team, so a team an organiser emptied with Remove person before this
-- feature shipped -- already correctly excluded from judging, results and dashboards --
-- stays excluded; only a team kept by this feature's own write path reappears.
ALTER TABLE event_teams ADD COLUMN IF NOT EXISTS kept_after_leave boolean NOT NULL DEFAULT false;
