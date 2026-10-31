package handler

import (
	"strings"
	"testing"
)

// db/migrations/zt-hack-teams-redesign.sql backfill, re-exercised directly:
// admin backfill (creator-still-on-team, and the fallback to the earliest
// joiner), the team size raise (both directions), locking a team already
// kept_after_leave, and the CHECK constraint the backfill must never
// violate. The harness is newHackApp (DB_DSN); setup goes through the API
// (which already maintains every invariant), then specific columns are
// regressed to a "before this migration ran" shape before re-running the
// migration's own idempotent statements.

func TestHackTeamsRedesignMigrationBackfillCreatorAdmin(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "migcreatorg")
	leader := a.newPerson(t, "migcreatleader")
	member := a.newPerson(t, "migcreatmember")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, member} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, member)
	// Regress to "before the migration": nobody marked admin, no tenure.
	if _, err := a.database.Exec(`UPDATE event_members SET team_admin=false, team_joined_at=NULL WHERE team_id=$1`, teamID); err != nil {
		t.Fatal(err)
	}
	// Re-run the migration's own backfill statements (idempotent).
	if _, err := a.database.Exec(`
		UPDATE event_members SET team_joined_at = joined_at
		 WHERE team_id IS NOT NULL AND team_joined_at IS NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`
		UPDATE event_members m SET team_admin = true
		  FROM event_teams t
		 WHERE m.team_id = t.id AND m.user_id = t.created_by`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`
		UPDATE event_members m SET team_admin = true
		 WHERE m.team_id IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM event_members a WHERE a.team_id = m.team_id AND a.team_admin)
		   AND m.user_id = (SELECT o.user_id FROM event_members o WHERE o.team_id = m.team_id
		                     ORDER BY o.team_joined_at, o.user_id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if !a.isTeamAdmin(t, leader) {
		t.Fatal("creator-still-on-team branch did not restore admin")
	}
	if a.isTeamAdmin(t, member) {
		t.Fatal("non-creator wrongly made admin")
	}
	var joinedAt, teamJoinedAt string
	if err := a.database.QueryRow(`SELECT joined_at::text, team_joined_at::text FROM event_members WHERE user_id=$1`, a.userID(t, leader)).Scan(&joinedAt, &teamJoinedAt); err != nil {
		t.Fatal(err)
	}
	if joinedAt != teamJoinedAt {
		t.Fatalf("tenure backfill: joined_at=%q team_joined_at=%q", joinedAt, teamJoinedAt)
	}
}

func TestHackTeamsRedesignMigrationBackfillEarliestJoinerFallback(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "migfallorg")
	leader := a.newPerson(t, "migfallleader")
	first := a.newPerson(t, "migfallfirst")
	second := a.newPerson(t, "migfallsecond")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	for _, p := range []person{leader, first, second} {
		a.joinParticipant(t, join, p, "P")
	}
	teamID, code := a.createTeamAndJoin(t, slug, leader, "Alpha")
	a.joinTeamByCode(t, slug, code, first)
	a.joinTeamByCode(t, slug, code, second)
	// The creator leaves (the current code already reassigns admin to the
	// earliest remaining member -- "first" -- which is exactly the state the
	// migration's fallback branch is meant to (re)produce from scratch).
	base := "/v1/hack/events/" + slug
	if r := a.at(t, "DELETE", base+"/people/"+a.userID(t, leader), nil, a.key(org)); r.status != 204 {
		t.Fatalf("remove leader: %d %s", r.status, r.body)
	}
	if !a.isTeamAdmin(t, first) {
		t.Fatal("setup: first should already be admin via EnsureTeamAdmin")
	}
	// Regress as if the migration had never run: no admin at all, but tenure
	// (team_joined_at) intact, since that is what the fallback's ORDER BY reads.
	if _, err := a.database.Exec(`UPDATE event_members SET team_admin=false WHERE team_id=$1`, teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`
		UPDATE event_members m SET team_admin = true
		  FROM event_teams t
		 WHERE m.team_id = t.id AND m.user_id = t.created_by`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`
		UPDATE event_members m SET team_admin = true
		 WHERE m.team_id IS NOT NULL
		   AND NOT EXISTS (SELECT 1 FROM event_members a WHERE a.team_id = m.team_id AND a.team_admin)
		   AND m.user_id = (SELECT o.user_id FROM event_members o WHERE o.team_id = m.team_id
		                     ORDER BY o.team_joined_at, o.user_id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	if !a.isTeamAdmin(t, first) {
		t.Fatal("fallback branch did not pick the earliest remaining joiner")
	}
	if a.isTeamAdmin(t, second) {
		t.Fatal("fallback branch picked the wrong member")
	}
}

func TestHackTeamsRedesignMigrationSizeRaise(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "migsizeorg")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	// Below 20: raised. At/above 20: left alone (never lowers a cap).
	if _, err := a.database.Exec(`UPDATE events SET team_size_max=2 WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	orgHigh := a.newPerson(t, "migsizeorghigh")
	slugHigh := uniqueSlug()
	a.createEvent(t, orgHigh, slugHigh, nil)
	if _, err := a.database.Exec(`UPDATE events SET team_size_max=30 WHERE slug=$1`, slugHigh); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE events SET team_size_max = 20 WHERE team_size_max < 20`); err != nil {
		t.Fatal(err)
	}
	var low, high int
	if err := a.database.QueryRow(`SELECT team_size_max FROM events WHERE slug=$1`, slug).Scan(&low); err != nil || low != 20 {
		t.Fatalf("below 20 not raised: %d %v", low, err)
	}
	if err := a.database.QueryRow(`SELECT team_size_max FROM events WHERE slug=$1`, slugHigh).Scan(&high); err != nil || high != 30 {
		t.Fatalf("above 20 was changed: %d %v", high, err)
	}
}

func TestHackTeamsRedesignMigrationLocksKeptTeams(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "migkeptorg")
	leader := a.newPerson(t, "migkeptleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	teamID, _ := a.createTeamAndJoin(t, slug, leader, "Alpha")
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 hour' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	if r := a.leave(t, slug, leader, map[string]any{"confirm": slug}); r.status != 200 {
		t.Fatalf("leave: %d %s", r.status, r.body)
	}
	var kept bool
	if err := a.database.QueryRow(`SELECT kept_after_leave FROM event_teams WHERE id=$1`, teamID).Scan(&kept); err != nil || !kept {
		t.Fatalf("setup: team not kept: %v %v", kept, err)
	}
	// Regress: as if this had happened before the migration, locked_at is
	// still unset even though kept_after_leave is true.
	if _, err := a.database.Exec(`UPDATE event_teams SET locked_at=NULL WHERE id=$1`, teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE event_teams SET locked_at = COALESCE(locked_at, now()) WHERE kept_after_leave`); err != nil {
		t.Fatal(err)
	}
	if !a.teamLocked(t, teamID) {
		t.Fatal("kept team not locked by the migration's backfill")
	}
}

// The CHECK is the hard backstop for a write path the
// event_members_team_admin_follows_team trigger does not cover (it only
// fires BEFORE UPDATE); disabling the trigger here isolates the CHECK
// itself, exactly the way a plain INSERT or a future write path would hit
// it with no trigger in between.
func TestHackTeamsRedesignCheckConstraintRejectsAdminOffTeam(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "migchkorg")
	leader := a.newPerson(t, "migchkleader")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, leader, "Leader")
	if _, err := a.database.Exec(`ALTER TABLE event_members DISABLE TRIGGER event_members_team_admin_follows_team`); err != nil {
		t.Fatal(err)
	}
	defer a.database.Exec(`ALTER TABLE event_members ENABLE TRIGGER event_members_team_admin_follows_team`)
	_, err := a.database.Exec(`UPDATE event_members SET team_admin=true WHERE user_id=$1 AND event_id=$2`, a.userID(t, leader), a.eventID(t, slug))
	if err == nil || !strings.Contains(err.Error(), "event_members_team_admin_chk") {
		t.Fatalf("expected the admin-off-team CHECK to reject this, got: %v", err)
	}
}
