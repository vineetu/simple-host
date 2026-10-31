package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrHackFindingStage = errors.New("teams cannot be changed at this stage")

var ErrHackFindingOff = errors.New("team finding is off")
var ErrHackFindingForbidden = errors.New("this team action is not available")

// LockFindingEvent serializes finding changes with the organiser's settings.
func LockFindingEvent(ctx context.Context, q Querier, eventID string) error {
	var enabled bool
	var stage string
	if err := q.QueryRowContext(ctx, `SELECT team_finding,stage FROM events WHERE id=$1 FOR SHARE`, eventID).Scan(&enabled, &stage); err != nil {
		return err
	}
	if !enabled {
		return ErrHackFindingOff
	}
	if stage != "open" && stage != "building" {
		return ErrHackFindingStage
	}
	return nil
}

// Read settings under the same event lock used by all finding writes.
func findingEvent(ctx context.Context, q Querier, ev Event) (Event, error) {
	if err := LockFindingEvent(ctx, q, ev.ID); err != nil {
		return ev, err
	}
	err := q.QueryRowContext(ctx, "SELECT team_size_max FROM events WHERE id=$1", ev.ID).Scan(&ev.TeamSizeMax)
	return ev, err
}

type TeamRequest struct {
	ID          string    `json:"id"`
	TeamID      string    `json:"team_id"`
	Team        string    `json:"team"`
	TeamName    string    `json:"team_name"`
	UserID      string    `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Kind        string    `json:"kind"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewTeamRequest locks the prospective member before the team, as code joining does.
func NewTeamRequest(ctx context.Context, q Querier, ev Event, actor, teamSlug, userID, kind string) (string, error) {
	ev, err := findingEvent(ctx, q, ev)
	if err != nil {
		return "", err
	}
	m, err := lockMember(ctx, q, ev.ID, userID)
	if err != nil {
		return "", err
	}
	status, err := MemberApprovalStatus(ctx, q, ev.ID, userID)
	if err != nil {
		return "", err
	}
	if m.Role != "participant" || status != "approved" || m.TeamID.Valid {
		return "", ErrHackFindingForbidden
	}
	t, err := lockTeamBySlug(ctx, q, ev.ID, teamSlug)
	if err != nil {
		return "", err
	}
	// A locked team takes nobody by request or invite (decision 9); "who can
	// join" (t.open) is no longer read at all -- every unlocked team is
	// askable, the former code-only teams simply join that list (section 5).
	if t.Locked() {
		return "", ErrHackTeamLocked
	}
	var looking, teammate bool
	if err = q.QueryRowContext(ctx, `SELECT m.looking, EXISTS(SELECT 1 FROM event_members a WHERE a.event_id=$1 AND a.user_id=$3 AND a.team_id=$2 AND a.approval_status='approved') FROM event_members m WHERE m.event_id=$1 AND m.user_id=$4`, ev.ID, t.ID, actor, userID).Scan(&looking, &teammate); err != nil {
		return "", err
	}
	if (kind == "request" && actor != userID) || (kind == "invite" && !teammate) {
		return "", ErrHackFindingForbidden
	}
	var existingID, existingKind string
	err = q.QueryRowContext(ctx, "SELECT id,kind FROM event_team_requests WHERE team_id=$1 AND user_id=$2", t.ID, userID).Scan(&existingID, &existingKind)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if existingID == "" && kind == "invite" && !looking {
		return "", ErrHackFindingForbidden
	}
	if err == nil && existingKind == kind {
		return "pending", nil
	}
	n, err := CountTeamMembers(ctx, q, t.ID)
	if err != nil {
		return "", err
	}
	if n >= ev.TeamSizeMax {
		return "", ErrHackTeamFull
	}
	state, err := TeamWriteStateFor(ctx, q, t.ID, true)
	if err != nil {
		return "", err
	}
	if state.Frozen() {
		return "", ErrHackTeamFrozen
	}
	if existingID != "" {
		if _, err = JoinTeamByCode(ctx, q, ev.ID, userID, t.Code, ev.TeamSizeMax); err != nil {
			return "", err
		}
		return "joined", nil
	}
	_, err = q.ExecContext(ctx, `INSERT INTO event_team_requests(event_id,team_id,user_id,kind,created_by) VALUES($1,$2,$3,$4,$5)`, ev.ID, t.ID, userID, kind, actor)
	return "created", err
}

// ResolveTeamRequest joins and clears all other pending offers in the same transaction.
func ResolveTeamRequest(ctx context.Context, q Querier, ev Event, actor, id, action string) error {
	ev, err := findingEvent(ctx, q, ev)
	if err != nil {
		return err
	}
	var teamID, userID, kind, code string
	err = q.QueryRowContext(ctx, `SELECT r.team_id,r.user_id,r.kind,t.code FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id WHERE r.event_id=$1 AND r.id=$2`, ev.ID, id).Scan(&teamID, &userID, &kind, &code)
	if err != nil {
		return err
	}
	// Keep the join lock order: prospective member, then team.
	if _, err = lockMember(ctx, q, ev.ID, userID); err != nil {
		return err
	}
	t, err := lockTeam(ctx, q, teamID)
	if err != nil {
		return err
	}
	code = t.Code
	var exists, teammate, actorAdmin bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_team_requests WHERE id=$1),
		EXISTS(SELECT 1 FROM event_members WHERE event_id=$2 AND user_id=$3 AND team_id=$4 AND approval_status='approved'),
		EXISTS(SELECT 1 FROM event_members WHERE event_id=$2 AND user_id=$3 AND team_id=$4 AND team_admin)`,
		id, ev.ID, actor, teamID).Scan(&exists, &teammate, &actorAdmin)
	if err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	// Accepting or declining a request needs a team admin now (was: any
	// teammate). Withdrawing a request stays the requester's own; cancelling
	// an invite stays any teammate's; accepting an invite stays the
	// invitee's own.
	if action == "withdraw" {
		if allowed := (kind == "request" && actor == userID) || (kind == "invite" && teammate); !allowed {
			return ErrHackFindingForbidden
		}
	} else if kind == "request" {
		if !actorAdmin {
			return ErrHackTeamAdminOnly
		}
	} else if actor != userID {
		return ErrHackFindingForbidden
	}
	if action == "accept" {
		if _, err = JoinTeamByCode(ctx, q, ev.ID, userID, code, ev.TeamSizeMax); err != nil {
			return err
		}
		st, err := TeamWriteStateFor(ctx, q, teamID, true)
		if err != nil {
			return err
		}
		if st.Frozen() {
			return ErrHackTeamFrozen
		}
	}
	_, err = q.ExecContext(ctx, `DELETE FROM event_team_requests WHERE id=$1`, id)
	return err
}

type FillPlacement struct {
	Team    string       `json:"team"`
	Name    string       `json:"name"`
	New     bool         `json:"new"`
	Members []FillPerson `json:"members"`
}
type FillPerson struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

// FillSkippedTeam is a team with room the planner deliberately passed over.
// Reported back so the preview can explain the skip ("Beta Builders isn't
// taking new people, so it was skipped.") instead of silently starting a
// new one-person team while that team still has room. Teams skipped because
// they're already at the target size need no explanation, so those aren't
// reported here.
type FillSkippedTeam struct {
	Team   string `json:"team"`
	Name   string `json:"name"`
	Reason string `json:"reason"` // currently always "locked"
}

// PlanFillTeams uses approved solo participants, oldest first: teams with
// room are topped up to size (not the event's team_size_max, so a Fill with
// a plain cap of 20 doesn't make one 20-person team of strangers), new
// teams get size. The caller validates 1 <= size <= team_size_max and locks
// the event and membership rows before preview or apply.
func PlanFillTeams(ctx context.Context, q Querier, ev Event, size int) ([]FillPlacement, []FillSkippedTeam, error) {
	rows, err := queryContext(ctx, q, `SELECT user_id,display_name FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL ORDER BY joined_at,user_id`, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	people := []FillPerson{}
	for rows.Next() {
		var p FillPerson
		if err = rows.Scan(&p.UserID, &p.DisplayName); err != nil {
			rows.Close()
			return nil, nil, err
		}
		people = append(people, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	teams, err := ListEventTeams(ctx, q, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	plan := []FillPlacement{}
	skipped := []FillSkippedTeam{}
	names := map[string]bool{}
	slugs := map[string]bool{}
	for _, t := range teams {
		names[t.Name] = true
		slugs[t.Slug] = true
		n, err := CountTeamMembers(ctx, q, t.ID)
		if err != nil {
			return nil, nil, err
		}
		if t.Locked() {
			if n < size && len(people) > 0 {
				skipped = append(skipped, FillSkippedTeam{Team: t.Slug, Name: t.Name, Reason: "locked"})
			}
			continue
		}
		st, err := TeamWriteStateFor(ctx, q, t.ID, false)
		if err != nil {
			return nil, nil, err
		}
		if st.Frozen() {
			continue
		}
		slots := size - n
		if slots <= 0 || len(people) == 0 {
			continue
		}
		if slots > len(people) {
			slots = len(people)
		}
		plan = append(plan, FillPlacement{Team: t.Slug, Name: t.Name, Members: append([]FillPerson{}, people[:slots]...)})
		people = people[slots:]
	}
	var past bool
	if err = q.QueryRowContext(ctx, "SELECT COALESCE(submission_deadline<=clock_timestamp(),false) FROM events WHERE id=$1", ev.ID).Scan(&past); err != nil {
		return nil, nil, err
	}
	if past {
		return plan, skipped, nil
	}
	for n := 1; len(people) > 0; n++ {
		name := fmt.Sprintf("Team %d", n)
		slug := fmt.Sprintf("team-%d", n)
		if names[name] || slugs[slug] {
			continue
		}
		var taken bool
		if err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_teams WHERE event_id=$1 AND lower(name)=lower($2))`, ev.ID, name).Scan(&taken); err != nil {
			return nil, nil, err
		}
		if taken {
			continue
		}
		used, err := TeamNameEverUsed(ctx, q, ev.Slug, ev.AccountID, slug)
		if err != nil {
			return nil, nil, err
		}
		if used {
			continue
		}
		slots := size
		if slots > len(people) {
			slots = len(people)
		}
		plan = append(plan, FillPlacement{Team: slug, Name: name, New: true, Members: append([]FillPerson{}, people[:slots]...)})
		people = people[slots:]
	}
	return plan, skipped, nil
}
