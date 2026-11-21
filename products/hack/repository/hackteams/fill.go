package hackteams

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type FillPlacement = model.FillPlacement
type FillPerson = model.FillPerson
type FillSkippedTeam = model.FillSkippedTeam

// NewTeamRequest asks to join a team (kind "request", by the participant)
// or invites a looking participant (kind "invite", by a teammate). When the
// opposite offer already waits it joins at once. The answer is "created",
// "pending" (the same offer already waits) or "joined".
func NewTeamRequest(ctx context.Context, q *sql.Tx, ev Event, actor, teamSlug, userID, kind string) (string, error) {
	ev, err := FindingEvent(ctx, q, ev)
	if err != nil {
		return "", err
	}
	t, err := lockRequestTeam(ctx, q, ev.ID, teamSlug, userID)
	if err != nil {
		return "", err
	}
	facts, err := readRequestFacts(ctx, q, ev.ID, t.ID, actor, userID)
	if err != nil {
		return "", err
	}
	if answer, err := requestDecision(facts, actor, userID, kind); answer != "" || err != nil {
		return answer, err
	}
	if err := checkTeamHasRoom(ctx, q, t.ID, ev.TeamSizeMax); err != nil {
		return "", err
	}
	if facts.existingID != "" {
		if _, err := JoinTeamByCode(ctx, q, ev.ID, userID, t.Code, ev.TeamSizeMax, EnsureTeamAdmin); err != nil {
			return "", err
		}
		return "joined", nil
	}
	_, err = q.ExecContext(ctx, `INSERT INTO event_team_requests(event_id,team_id,user_id,kind,created_by) VALUES($1,$2,$3,$4,$5)`, ev.ID, t.ID, userID, kind, actor)
	return "created", err
}

// lockRequestTeam locks the prospective member, then the team, as code
// joining does. Only an approved participant without a team may be asked
// for, and a locked team takes nobody by request or invite;
// every unlocked team is askable.
func lockRequestTeam(ctx context.Context, q *sql.Tx, eventID, teamSlug, userID string) (EventTeam, error) {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	status, err := hackevents.MemberApprovalStatus(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" || status != "approved" || m.TeamID.Valid {
		return EventTeam{}, ErrHackFindingForbidden
	}
	t, err := LockTeamBySlug(ctx, q, eventID, teamSlug)
	if err == nil && t.Locked() {
		return EventTeam{}, ErrHackTeamLocked
	}
	return t, err
}

// requestFacts is who stands where for one request or invite: whether the
// participant is looking, whether the actor is on the team, and any offer
// already waiting between them.
type requestFacts struct {
	looking, teammate        bool
	existingID, existingKind string
}

func readRequestFacts(ctx context.Context, q *sql.Tx, eventID, teamID, actor, userID string) (f requestFacts, err error) {
	if err = q.QueryRowContext(ctx, `SELECT m.looking, EXISTS(SELECT 1 FROM event_members a WHERE a.event_id=$1 AND a.user_id=$3 AND a.team_id=$2 AND a.approval_status='approved') FROM event_members m WHERE m.event_id=$1 AND m.user_id=$4`, eventID, teamID, actor, userID).Scan(&f.looking, &f.teammate); err != nil {
		return f, err
	}
	err = q.QueryRowContext(ctx, "SELECT id,kind FROM event_team_requests WHERE team_id=$1 AND user_id=$2", teamID, userID).Scan(&f.existingID, &f.existingKind)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return f, err
}

// requestDecision checks who may send this offer: a request is the
// participant's own, an invite comes from a teammate and goes only to
// someone looking (or answering their request). It answers "pending" when
// the same offer already waits, and "" to go ahead.
func requestDecision(f requestFacts, actor, userID, kind string) (string, error) {
	switch {
	case kind == "request" && actor != userID, kind == "invite" && !f.teammate:
		return "", ErrHackFindingForbidden
	case kind == "invite" && f.existingID == "" && !f.looking:
		return "", ErrHackFindingForbidden
	case f.existingID != "" && f.existingKind == kind:
		return "pending", nil
	}
	return "", nil
}

// checkTeamHasRoom refuses a full team or one frozen past its deadline.
func checkTeamHasRoom(ctx context.Context, q *sql.Tx, teamID string, sizeMax int) error {
	n, err := CountTeamMembers(ctx, q, teamID)
	if err != nil {
		return err
	}
	if n >= sizeMax {
		return ErrHackTeamFull
	}
	state, err := TeamWriteStateFor(ctx, q, teamID, true)
	if err == nil && state.Frozen() {
		return ErrHackTeamFrozen
	}
	return err
}

// ResolveTeamRequest joins and clears all other pending offers in the same transaction.
func ResolveTeamRequest(ctx context.Context, q *sql.Tx, ev Event, actor, id, action string) error {
	ev, err := FindingEvent(ctx, q, ev)
	if err != nil {
		return err
	}
	var teamID, userID, kind, code string
	err = q.QueryRowContext(ctx, `SELECT r.team_id,r.user_id,r.kind,t.code FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id WHERE r.event_id=$1 AND r.id=$2`, ev.ID, id).Scan(&teamID, &userID, &kind, &code)
	if err != nil {
		return err
	}
	// Keep the join lock order: prospective member, then team.
	if _, err = LockMember(ctx, q, ev.ID, userID); err != nil {
		return err
	}
	t, err := LockTeam(ctx, q, teamID)
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
	if err := mayResolve(action, kind, actor, userID, teammate, actorAdmin); err != nil {
		return err
	}
	if action == "accept" {
		if _, err = JoinTeamByCode(ctx, q, ev.ID, userID, code, ev.TeamSizeMax, EnsureTeamAdmin); err != nil {
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

// mayResolve says who may act on a pending offer. Withdrawing a request is
// the requester's own and cancelling an invite any teammate's; accepting or
// declining a request needs a team admin; accepting or declining an invite
// is the invitee's own.
func mayResolve(action, kind, actor, userID string, teammate, actorAdmin bool) error {
	switch {
	case action == "withdraw" && kind == "request" && actor == userID, action == "withdraw" && kind == "invite" && teammate:
		return nil
	case action == "withdraw":
		return ErrHackFindingForbidden
	case kind == "request" && !actorAdmin:
		return ErrHackTeamAdminOnly
	case kind != "request" && actor != userID:
		return ErrHackFindingForbidden
	}
	return nil
}

// PlanFillTeams uses approved solo participants, oldest first: teams with
// room are topped up to size (not the event's team_size_max, so a Fill with
// a plain cap of 20 doesn't make one 20-person team of strangers), and
// before the submission deadline new teams get size. The caller validates
// 1 <= size <= team_size_max and locks the event and membership rows before
// preview or apply.
func PlanFillTeams(ctx context.Context, q Querier, ev Event, size int, siteEverNamed func(context.Context, Querier, string, string) (bool, error)) ([]FillPlacement, []FillSkippedTeam, error) {
	people, err := soloParticipants(ctx, q, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	teams, err := ListEventTeams(ctx, q, ev.ID)
	if err != nil {
		return nil, nil, err
	}
	plan, skipped, people, err := topUpTeams(ctx, q, teams, size, people)
	if err != nil {
		return nil, nil, err
	}
	var past bool
	if err = q.QueryRowContext(ctx, "SELECT COALESCE(submission_deadline<=clock_timestamp(),false) FROM events WHERE id=$1", ev.ID).Scan(&past); err != nil || past {
		return plan, skipped, err
	}
	fresh, err := newFillTeams(ctx, q, ev, teams, size, people, siteEverNamed)
	if err != nil {
		return nil, nil, err
	}
	return append(plan, fresh...), skipped, nil
}

// soloParticipants lists approved participants without a team, oldest first.
func soloParticipants(ctx context.Context, q Querier, eventID string) ([]FillPerson, error) {
	rows, err := queryContext(ctx, q, `SELECT m.user_id,COALESCE(u.display_name,'') FROM event_members m JOIN users u ON u.id=m.user_id WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL ORDER BY joined_at,user_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := []FillPerson{}
	for rows.Next() {
		var p FillPerson
		if err := rows.Scan(&p.UserID, &p.DisplayName); err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	return people, rows.Err()
}

// topUpTeams fills each open, unfrozen team with room up to size and
// returns the people left over. A locked team with room is reported as
// skipped while anyone is left to place.
func topUpTeams(ctx context.Context, q Querier, teams []EventTeam, size int, people []FillPerson) (plan []FillPlacement, skipped []FillSkippedTeam, rest []FillPerson, err error) {
	plan, skipped = []FillPlacement{}, []FillSkippedTeam{}
	for _, t := range teams {
		n, err := CountTeamMembers(ctx, q, t.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		if t.Locked() {
			if n < size && len(people) > 0 {
				skipped = append(skipped, FillSkippedTeam{Team: t.Slug, Name: t.Name, Reason: "locked"})
			}
			continue
		}
		st, err := TeamWriteStateFor(ctx, q, t.ID, false)
		if err != nil {
			return nil, nil, nil, err
		}
		if st.Frozen() || n >= size || len(people) == 0 {
			continue
		}
		var members []FillPerson
		members, people = takePeople(people, size-n)
		plan = append(plan, FillPlacement{Team: t.Slug, Name: t.Name, Members: members})
	}
	return plan, skipped, people, nil
}

// newFillTeams places everyone left into new "Team N" teams of size, skipping
// any name or site name an existing or earlier team has used.
func newFillTeams(ctx context.Context, q Querier, ev Event, teams []EventTeam, size int, people []FillPerson, siteEverNamed func(context.Context, Querier, string, string) (bool, error)) ([]FillPlacement, error) {
	names, slugs := map[string]bool{}, map[string]bool{}
	for _, t := range teams {
		names[t.Name], slugs[t.Slug] = true, true
	}
	var plan []FillPlacement
	for n := 1; len(people) > 0; n++ {
		name, slug := fmt.Sprintf("Team %d", n), fmt.Sprintf("team-%d", n)
		if names[name] || slugs[slug] {
			continue
		}
		free, err := fillNameFree(ctx, q, ev, name, slug, siteEverNamed)
		if err != nil {
			return nil, err
		}
		if !free {
			continue
		}
		var members []FillPerson
		members, people = takePeople(people, size)
		plan = append(plan, FillPlacement{Team: slug, Name: name, New: true, Members: members})
	}
	return plan, nil
}

// fillNameFree reports whether no team has name (in any case) and no site
// was ever named slug in the event.
func fillNameFree(ctx context.Context, q Querier, ev Event, name, slug string, siteEverNamed func(context.Context, Querier, string, string) (bool, error)) (bool, error) {
	var taken bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_teams WHERE event_id=$1 AND lower(name)=lower($2))`, ev.ID, name).Scan(&taken); err != nil || taken {
		return false, err
	}
	used, err := TeamNameEverUsed(ctx, q, ev.Slug, ev.AccountID, slug, siteEverNamed)
	return err == nil && !used, err
}

// takePeople splits off the first n people (or all, if fewer) as a copy.
func takePeople(people []FillPerson, n int) (taken, rest []FillPerson) {
	n = min(n, len(people))
	return append([]FillPerson{}, people[:n]...), people[n:]
}
