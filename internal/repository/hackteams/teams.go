package hackteams

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"sort"
	"strings"
)

// Querier aliases the shared SQL interface.
type Querier = sqlkit.Querier

// EventTeam aliases the shared model record.
type EventTeam = model.EventTeam

// EventMember aliases the shared model record.
type EventMember = model.EventMember

// EventPerson aliases the shared model record.
type EventPerson = model.EventPerson

// Sentinel errors for hosted events. Handlers map these to the HTTP contract.
var (
	ErrHackNameTaken             = errors.New("name taken")
	ErrHackTeamNotFound          = errors.New("team not found")
	ErrHackTeamFull              = errors.New("team full")
	ErrHackAlreadyInTeam         = errors.New("already in team")
	ErrHackCannotRemoveOrganiser = hackevents.ErrHackCannotRemoveOrganiser
	ErrHackNotParticipant        = errors.New("not a participant")
	ErrHackTeamFrozen            = errors.New("team past its deadline")
	// Teams redesign (zt-hack-teams-redesign.sql).
	ErrHackTeamLocked    = errors.New("team is locked")
	ErrHackTeamKept      = errors.New("team can't take new members")
	ErrHackLastTeamAdmin = errors.New("can't unmake the last admin")
	ErrHackTeamAdminOnly = errors.New("only a team admin can do that")
)

const teamColumns = `id, event_id, slug, name, code, created_by, created_at,
	deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason,
	kept_after_leave, locked_at, locked_by`

const memberForUpdate = `
	SELECT event_id, user_id, role, display_name, team_id, coc_accepted_at, joined_at
	  FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`

var teamForUpdate = `
	SELECT ` + teamColumns + `
	  FROM event_teams WHERE id = $1 FOR UPDATE`

// teamCodeAlphabet and teamCodeLen mirror the alphabet in
// internal/rules/hackevents/codes.go and team length in internal/hackhttp/hack.go.
// Account erasure's rotation keeps these constants here.
const (
	teamCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	teamCodeLen      = 8
)

const effectiveDeadlineSQL = hackevents.EffectiveDeadlineSQL

// TeamKeepableSQL is the predicate for deleting an empty team.
const TeamKeepableSQL = hackevents.TeamKeepableSQL

func queryContext(ctx context.Context, q Querier, query string, args ...any) (*sql.Rows, error) {
	switch t := q.(type) {
	case *sql.DB:
		return t.QueryContext(ctx, query, args...)
	case *sql.Tx:
		return t.QueryContext(ctx, query, args...)
	default:
		return nil, fmt.Errorf("db: querier does not support QueryContext")
	}
}
func scanMember(row *sql.Row) (EventMember, error) {
	var m EventMember
	err := row.Scan(&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt)
	return m, err
}

// isUniqueViolation detects a Postgres unique-constraint violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq and pgx both surface the SQLSTATE in the error text as "23505".
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}

// ListEventTeams is every team of the event, oldest first.
func ListEventTeams(ctx context.Context, q Querier, eventID string) ([]EventTeam, error) {
	rows, err := queryContext(ctx, q, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 ORDER BY created_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventTeam
	for rows.Next() {
		t, err := scanTeamRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func scanTeamFields(t *EventTeam) []any {
	return []any{&t.ID, &t.EventID, &t.Slug, &t.Name, &t.Code, &t.CreatedBy, &t.CreatedAt,
		&t.DeadlineOverride, &t.PinnedVersion, &t.PinnedAt, &t.SiteTakenDownAt, &t.SiteTakenDownReason,
		&t.KeptAfterLeave, &t.LockedAt, &t.LockedBy}
}

func scanTeam(row *sql.Row) (EventTeam, error) {
	var t EventTeam
	err := row.Scan(scanTeamFields(&t)...)
	return t, err
}

func scanTeamRow(rows *sql.Rows) (EventTeam, error) {
	var t EventTeam
	err := rows.Scan(scanTeamFields(&t)...)
	return t, err
}

// GetEventTeamByID loads a team by primary key.
func GetEventTeamByID(ctx context.Context, q Querier, teamID string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE id = $1`, teamID))
}

// GetEventTeamBySlug loads a team of an event by its slug.
func GetEventTeamBySlug(ctx context.Context, q Querier, eventID, slug string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, slug))
}

// GetEventTeamByCode loads a team of an event by its (normalised) join code.
func GetEventTeamByCode(ctx context.Context, q Querier, eventID, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND code = $2`, eventID, code))
}

// GetEventTeamByCodeOnly loads a team by its (normalised) code alone: team
// codes are unique across every event (event_teams.code UNIQUE), which is
// what the public team link (GET /v1/hack/team-links/{code}) relies on --
// the code alone names a team without an event slug in the URL.
func GetEventTeamByCodeOnly(ctx context.Context, q Querier, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE code = $1`, code))
}

// ListTeamPeople is the members of one team as the organiser sees them.
func ListTeamPeople(ctx context.Context, q Querier, teamID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, u.username, m.display_name, m.role, m.team_id,
		       NULL, NULL, m.joined_at, m.coc_accepted_at, m.approval_status, m.team_admin
		  FROM event_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.team_id = $1
		 ORDER BY m.joined_at ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPerson
	for rows.Next() {
		var p EventPerson
		if err := rows.Scan(&p.UserID, &p.Email, &p.DisplayName, &p.Role, &p.TeamID,
			&p.TeamSlug, &p.TeamName, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus, &p.Admin); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountTeamMembers is how many people are on a team.
func CountTeamMembers(ctx context.Context, q Querier, teamID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_members WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

// TeamSlugExists reports whether the event already has a team with slug.
func TeamSlugExists(ctx context.Context, q Querier, eventID, slug string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND slug = $2)`, eventID, slug).Scan(&taken)
	return taken, err
}

// InsertEventTeam writes a team. Unique on (event, slug) or code is returned as-is.
func InsertEventTeam(ctx context.Context, q Querier, eventID, slug, name, code, createdBy string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		INSERT INTO event_teams (event_id, slug, name, code, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+teamColumns,
		eventID, slug, name, code, createdBy))
}

// DeleteEventTeam removes the team. Members' team_id becomes NULL (ON DELETE SET NULL).
func DeleteEventTeam(ctx context.Context, q Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_teams WHERE id = $1`, teamID)
	return err
}

// LockMember exposes the team operation for transitional callers.
func LockMember(ctx context.Context, q Querier, eventID, userID string) (EventMember, error) {
	return scanMember(q.QueryRowContext(ctx, memberForUpdate, eventID, userID))
}

// LockTeam exposes the team operation for transitional callers.
func LockTeam(ctx context.Context, q Querier, teamID string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, teamForUpdate, teamID))
}

// LockTeamByCode exposes the team operation for transitional callers.
func LockTeamByCode(ctx context.Context, q Querier, eventID, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND code = $2 FOR UPDATE`, eventID, code))
}

// LockTeamBySlug exposes the team operation for transitional callers.
func LockTeamBySlug(ctx context.Context, q Querier, eventID, slug string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND slug = $2 FOR UPDATE`, eventID, slug))
}

// SetMemberTeam puts the member on teamID, or takes them off (teamID invalid).
// Joining (any team, including one just created) always starts as a plain
// member -- team_admin false, team_joined_at now -- the caller promotes a
// fresh creator afterwards (CreateTeamAndJoin). Leaving clears both, so a
// returning member's tenure and admin status never carry over from an
// earlier stint on the same team.
func SetMemberTeam(ctx context.Context, q Querier, eventID, userID string, teamID sql.NullString) error {
	var err error
	if teamID.Valid {
		_, err = q.ExecContext(ctx, `
			UPDATE event_members SET team_id = $3, team_removal_notice = '',
				team_joined_at = clock_timestamp(), team_admin = false
			 WHERE event_id = $1 AND user_id = $2`,
			eventID, userID, teamID.String)
	} else {
		_, err = q.ExecContext(ctx, `
			UPDATE event_members SET team_id = NULL, team_removal_notice = '',
				team_joined_at = NULL, team_admin = false
			 WHERE event_id = $1 AND user_id = $2`,
			eventID, userID)
	}
	if err == nil && teamID.Valid {
		_, err = q.ExecContext(ctx, `DELETE FROM event_team_requests WHERE event_id=$1 AND user_id=$2`, eventID, userID)
		if err == nil {
			_, err = q.ExecContext(ctx, `UPDATE event_members SET looking=false, looking_intro='' WHERE event_id=$1 AND user_id=$2`, eventID, userID)
		}
	}
	return err
}

// AdminTeamIDs is every team userID currently admins, for a caller whose
// own deletion of the member row does not go through a hack write path at
// all: account erasure (EraseAccount) deletes the person's users row, and
// event_members.user_id's ON DELETE CASCADE takes every membership with it,
// including team_admin, with nothing to run the ordinary leave cleanup
// afterward (Codex review, 2026-11-01; widened by Grok security review,
// 2026-11-01, to the full DeleteTeamIfEmpty/kept/EnsureTeamAdmin sequence
// LeaveMembership runs, not EnsureTeamAdmin alone -- the erased account
// could have been a team's only member). The caller reads this before the
// cascade and runs that sequence on each team after it.
func AdminTeamIDs(ctx context.Context, q Querier, userID string) ([]string, error) {
	rows, err := queryContext(ctx, q, `SELECT team_id FROM event_members WHERE user_id = $1 AND team_admin AND team_id IS NOT NULL`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func randomTeamCode() (string, error) {
	max := 256 - (256 % len(teamCodeAlphabet))
	b := make([]byte, teamCodeLen)
	for i := range b {
		var buf [1]byte
		for {
			if _, err := rand.Read(buf[:]); err != nil {
				return "", err
			}
			if int(buf[0]) < max {
				b[i] = teamCodeAlphabet[int(buf[0])%len(teamCodeAlphabet)]
				break
			}
		}
	}
	return string(b), nil
}

// RotateTeamCodeTx gives teamID a new code, retrying under a savepoint on a
// collision (same pattern as claimHandleIn) so it never aborts the larger
// transaction account erasure runs in. Mirrors the rotation helper in
// internal/app/hack/teams/organiser.go, which the repository cannot import.
func RotateTeamCodeTx(ctx context.Context, tx *sql.Tx, teamID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := randomTeamCode()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "SAVEPOINT rotate_team_code"); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE event_teams SET code = $2 WHERE id = $1`, teamID, code); err == nil {
			_, err = tx.ExecContext(ctx, "RELEASE SAVEPOINT rotate_team_code")
			return err
		}
		if !isUniqueViolation(err) {
			return err
		}
		if _, rerr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT rotate_team_code"); rerr != nil {
			return rerr
		}
		if _, rerr := tx.ExecContext(ctx, "RELEASE SAVEPOINT rotate_team_code"); rerr != nil {
			return rerr
		}
	}
	return errors.New("could not find a free team code")
}

// DeleteTeamIfEmpty exposes the team operation for transitional callers.
func DeleteTeamIfEmpty(ctx context.Context, q Querier, teamID string) error {
	n, err := CountTeamMembers(ctx, q, teamID)
	if err != nil {
		return err
	}
	if n != 0 {
		return nil
	}
	// A team whose deadline has passed (pinned or about to be) stays, empty: its submission
	// is kept for judging (the organiser can still remove it).
	_, err = q.ExecContext(ctx, `
		DELETE FROM event_teams t USING events e
		 WHERE t.id = $1 AND e.id = t.event_id AND `+TeamKeepableSQL, teamID)
	return err
}

// CreateTeamAndJoin inserts a team and puts the participant on it. The member
// row is locked so a concurrent join cannot land them on two teams.
func CreateTeamAndJoin(ctx context.Context, q Querier, eventID, userID, slug, name, code string) (EventTeam, error) {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" {
		return EventTeam{}, ErrHackNotParticipant
	}
	status, err := hackevents.MemberApprovalStatus(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if status != "approved" {
		return EventTeam{}, ErrHackNotParticipant
	}
	if m.TeamID.Valid {
		return EventTeam{}, ErrHackAlreadyInTeam
	}
	team, err := InsertEventTeam(ctx, q, eventID, slug, name, code, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if err := SetMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	// The creator is the team's first admin.
	if _, err := q.ExecContext(ctx, `UPDATE event_members SET team_admin = true WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return EventTeam{}, err
	}
	return team, nil
}

// JoinTeamByCode puts a participant on the team with code. Same team is a no-op.
// sizeMax is events.team_size_max. The team and member rows are locked.
func JoinTeamByCode(ctx context.Context, q Querier, eventID, userID, code string, sizeMax int, ensureTeamAdmin func(context.Context, Querier, string) error) (EventTeam, error) {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" {
		return EventTeam{}, ErrHackNotParticipant
	}
	status, err := hackevents.MemberApprovalStatus(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if status != "approved" {
		return EventTeam{}, ErrHackNotParticipant
	}
	team, err := LockTeamByCode(ctx, q, eventID, code)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil {
		return EventTeam{}, err
	}
	if m.TeamID.Valid {
		if m.TeamID.String == team.ID {
			return team, nil
		}
		return EventTeam{}, ErrHackAlreadyInTeam
	}
	if team.Locked() {
		return EventTeam{}, ErrHackTeamLocked
	}
	n, err := CountTeamMembers(ctx, q, team.ID)
	if err != nil {
		return EventTeam{}, err
	}
	if n >= sizeMax {
		return EventTeam{}, ErrHackTeamFull
	}
	if err := SetMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	// Backstop (Grok security review, 2026-11-01): a team with members but
	// no admin should not normally exist by the time anyone can join it --
	// every exit path runs ensureTeamAdmin -- but code joining is the one
	// way IN, so it is the cheapest place to also guarantee the invariant
	// coming out the other side, a no-op when an admin is already there.
	if err := ensureTeamAdmin(ctx, q, team.ID); err != nil {
		return EventTeam{}, err
	}
	return team, nil
}

// LeaveTeam takes the participant off their team, deletes the team if empty,
// and promotes a new admin when leaving emptied the team of admins but not
// of members.
func LeaveTeam(ctx context.Context, q Querier, eventID, userID string, ensureTeamAdmin func(context.Context, Querier, string) error) error {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if !m.TeamID.Valid {
		return nil
	}
	if _, err := LockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	// Past the team's deadline its members stay (checked under the team
	// row's lock): leaving could empty it and lose its submission.
	var frozen bool
	if err := q.QueryRowContext(ctx, `
		SELECT COALESCE(`+effectiveDeadlineSQL+` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, m.TeamID.String).Scan(&frozen); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if frozen {
		return ErrHackTeamFrozen
	}
	team := m.TeamID.String
	if err := SetMemberTeam(ctx, q, eventID, userID, sql.NullString{}); err != nil {
		return err
	}
	if err := DeleteTeamIfEmpty(ctx, q, team); err != nil {
		return err
	}
	return ensureTeamAdmin(ctx, q, team)
}

// MoveParticipantToTeam is the organiser putting a participant on a team
// (from any team or none). sizeMax is events.team_size_max.
func MoveParticipantToTeam(ctx context.Context, q Querier, eventID, userID, teamSlug string, sizeMax int, ensureTeamAdmin func(context.Context, Querier, string) error) (EventTeam, error) {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if m.Role != "participant" {
		return EventTeam{}, ErrHackNotParticipant
	}
	status, err := hackevents.MemberApprovalStatus(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	if status != "approved" {
		return EventTeam{}, ErrHackNotParticipant
	}
	dest, err := GetEventTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil {
		return EventTeam{}, err
	}
	if m.TeamID.Valid && m.TeamID.String == dest.ID {
		return dest, nil
	}
	ids := []string{dest.ID}
	if m.TeamID.Valid {
		ids = append(ids, m.TeamID.String)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if _, err := LockTeam(ctx, q, id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return EventTeam{}, err
		}
	}
	team, err := LockTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil {
		return EventTeam{}, err
	}
	// A team kept after its last member left can never take anyone again,
	// by hand or otherwise; a plain lock is not this check -- the organiser
	// may still place someone on an ordinary locked team.
	if team.KeptAfterLeave {
		return EventTeam{}, ErrHackTeamKept
	}
	old := m.TeamID
	n, err := CountTeamMembers(ctx, q, team.ID)
	if err != nil {
		return EventTeam{}, err
	}
	if n >= sizeMax {
		return EventTeam{}, ErrHackTeamFull
	}
	if err := SetMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	if old.Valid && old.String != team.ID {
		if err := DeleteTeamIfEmpty(ctx, q, old.String); err != nil {
			return EventTeam{}, err
		}
		if err := ensureTeamAdmin(ctx, q, old.String); err != nil {
			return EventTeam{}, err
		}
	}
	if err := ensureTeamAdmin(ctx, q, team.ID); err != nil {
		return EventTeam{}, err
	}
	return team, nil
}

// RemoveParticipantFromTeam takes a participant off a team; they stay in the event.
func RemoveParticipantFromTeam(ctx context.Context, q Querier, eventID, teamSlug, userID string, ensureTeamAdmin func(context.Context, Querier, string) error) error {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	team, err := LockTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHackTeamNotFound
	}
	if err != nil {
		return err
	}
	if !m.TeamID.Valid || m.TeamID.String != team.ID {
		return sql.ErrNoRows
	}
	if err := SetMemberTeam(ctx, q, eventID, userID, sql.NullString{}); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `UPDATE event_members SET team_removal_notice=$3 WHERE event_id=$1 AND user_id=$2`, eventID, userID, "The organiser took you off "+team.Name+"."); err != nil {
		return err
	}
	if err := DeleteTeamIfEmpty(ctx, q, team.ID); err != nil {
		return err
	}
	return ensureTeamAdmin(ctx, q, team.ID)
}
