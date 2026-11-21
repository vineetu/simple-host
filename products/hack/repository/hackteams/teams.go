package hackteams

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type Querier = sqlkit.Querier

type EventTeam = model.EventTeam

type EventMember = model.EventMember

type EventPerson = model.EventPerson

// Sentinel errors for hosted events. Handlers map these to the HTTP contract.
var (
	ErrHackTeamNotFound          = errors.New("team not found")
	ErrHackTeamFull              = errors.New("team full")
	ErrHackAlreadyInTeam         = errors.New("already in team")
	ErrHackCannotRemoveOrganiser = hackevents.ErrHackCannotRemoveOrganiser
	ErrHackNotParticipant        = errors.New("not a participant")
	ErrHackTeamFrozen            = errors.New("team past its deadline")
	ErrHackTeamLocked            = errors.New("team is locked")
	ErrHackTeamKept              = errors.New("team can't take new members")
	ErrHackLastTeamAdmin         = errors.New("can't unmake the last admin")
	ErrHackTeamAdminOnly         = errors.New("only a team admin can do that")
)

const teamColumns = `id, event_id, slug, name, code, created_by, created_at,
	deadline_override, pinned_version, pinned_at, site_taken_down_at, site_taken_down_reason,
	kept_after_leave, locked_at, locked_by`

const memberForUpdate = `
	SELECT m.event_id, m.user_id, m.role, '', m.team_id, m.coc_accepted_at, m.joined_at
	  FROM event_members m WHERE m.event_id = $1 AND m.user_id = $2 FOR UPDATE OF m`

var teamForUpdate = `
	SELECT ` + teamColumns + `
	  FROM event_teams WHERE id = $1 FOR UPDATE`

// teamCodeAlphabet and teamCodeLen mirror the alphabet in
// host/rules/hackevents/codes.go and team length in host/app/hack/teams/service.go (teamCodeLength).
// Account erasure's rotation keeps these constants here.
const (
	teamCodeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	teamCodeLen      = 8
)

const effectiveDeadlineSQL = hackevents.EffectiveDeadlineSQL

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

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq and pgx both surface the SQLSTATE in the error text as "23505".
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}

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

func GetEventTeamByID(ctx context.Context, q Querier, teamID string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE id = $1`, teamID))
}

func GetEventTeamBySlug(ctx context.Context, q Querier, eventID, slug string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, slug))
}

func GetEventTeamByCode(ctx context.Context, q Querier, eventID, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND code = $2`, eventID, code))
}

// GetEventTeamByCodeOnly loads a team by its code alone; codes are unique across events.
func GetEventTeamByCodeOnly(ctx context.Context, q Querier, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE code = $1`, code))
}

func ListTeamPeople(ctx context.Context, q Querier, teamID string) ([]EventPerson, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, m.role, m.team_id,
		       NULL, NULL, m.joined_at, m.coc_accepted_at, m.approval_status, m.team_admin
		  FROM event_members m
		 WHERE m.team_id = $1
		 ORDER BY m.joined_at ASC`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventPerson
	for rows.Next() {
		var p EventPerson
		if err := rows.Scan(&p.UserID, &p.Role, &p.TeamID,
			&p.TeamSlug, &p.TeamName, &p.JoinedAt, &p.CocAcceptedAt, &p.ApprovalStatus, &p.Admin); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hackevents.WithAccounts(ctx, q, out)
}

func CountTeamMembers(ctx context.Context, q Querier, teamID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_members WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

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

func LockMember(ctx context.Context, q Querier, eventID, userID string) (EventMember, error) {
	m, err := scanMember(q.QueryRowContext(ctx, memberForUpdate, eventID, userID))
	return hackevents.WithDisplayName(ctx, q, m, err)
}

func LockTeam(ctx context.Context, q Querier, teamID string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, teamForUpdate, teamID))
}

func LockTeamByCode(ctx context.Context, q Querier, eventID, code string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND code = $2 FOR UPDATE`, eventID, code))
}

func LockTeamBySlug(ctx context.Context, q Querier, eventID, slug string) (EventTeam, error) {
	return scanTeam(q.QueryRowContext(ctx, `
		SELECT `+teamColumns+`
		  FROM event_teams WHERE event_id = $1 AND slug = $2 FOR UPDATE`, eventID, slug))
}

// SetMemberTeam puts the member on teamID as a plain member, or takes them off (teamID invalid), resetting tenure and admin.
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

// AdminTeamIDs is every team userID admins, read before account erasure cascades their memberships away.
func AdminTeamIDs(ctx context.Context, q Querier, userID string) ([]string, error) {
	return userIDs(ctx, q, `SELECT team_id FROM event_members WHERE user_id = $1 AND team_admin AND team_id IS NOT NULL`, userID)
}

// userIDs runs a query of one id column and returns the ids in order.
func userIDs(ctx context.Context, q Querier, query string, args ...any) ([]string, error) {
	rows, err := queryContext(ctx, q, query, args...)
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

// RotateTeamCodeTx gives teamID a new code, retrying a collision under a savepoint.
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
	// Backstop: a team with members but no admin should not normally exist by the
	// time anyone can join it (every exit path runs ensureTeamAdmin), but code
	// joining is the one way in, so it is the cheapest place to also guarantee the
	// invariant; a no-op when an admin is already there.
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
	m, err := lockMovable(ctx, q, eventID, userID)
	if err != nil {
		return EventTeam{}, err
	}
	dest, err := GetEventTeamBySlug(ctx, q, eventID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return EventTeam{}, ErrHackTeamNotFound
	}
	if err != nil || (m.TeamID.Valid && m.TeamID.String == dest.ID) {
		return dest, err
	}
	team, err := lockMoveTeams(ctx, q, eventID, teamSlug, dest.ID, m.TeamID)
	if err != nil {
		return EventTeam{}, err
	}
	if err := checkRoom(ctx, q, team, sizeMax); err != nil {
		return EventTeam{}, err
	}
	if err := SetMemberTeam(ctx, q, eventID, userID, sql.NullString{String: team.ID, Valid: true}); err != nil {
		return EventTeam{}, err
	}
	if old := m.TeamID; old.Valid && old.String != team.ID {
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

// lockMovable locks the member a move names; only an approved participant
// can be moved.
func lockMovable(ctx context.Context, q Querier, eventID, userID string) (EventMember, error) {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return m, err
	}
	if m.Role != "participant" {
		return m, ErrHackNotParticipant
	}
	status, err := hackevents.MemberApprovalStatus(ctx, q, eventID, userID)
	if err != nil {
		return m, err
	}
	if status != "approved" {
		return m, ErrHackNotParticipant
	}
	return m, nil
}

// lockMoveTeams locks the destination and the member's old team in id
// order, then re-reads the destination under its lock.
func lockMoveTeams(ctx context.Context, q Querier, eventID, teamSlug, destID string, old sql.NullString) (EventTeam, error) {
	ids := []string{destID}
	if old.Valid {
		ids = append(ids, old.String)
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
	return team, err
}

// checkRoom refuses a full team or one kept after its last member left (a plain lock does not stop the organiser).
func checkRoom(ctx context.Context, q Querier, team EventTeam, sizeMax int) error {
	if team.KeptAfterLeave {
		return ErrHackTeamKept
	}
	n, err := CountTeamMembers(ctx, q, team.ID)
	if err != nil {
		return err
	}
	if n >= sizeMax {
		return ErrHackTeamFull
	}
	return nil
}

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
