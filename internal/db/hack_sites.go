package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Team sites, member keys and the submission deadline (hosted events, M2;
// db/migrations/hack2-team-sites.sql, docs/designs/simple-hack-platform.md).
//
// A team's site is the site named after the team (event_teams.slug) of the
// event's holding account (events.account_id). A member deploys with a key of
// scope KeyScopeTeam, which belongs to the member and acts as the holding
// account on that one site, only while the member is a participant on that
// team.

// KeyScopeTeam is a member's key for their team's site.
const KeyScopeTeam = "team"

// ErrTeamKeyInactive: a team key whose person is no longer a participant on
// its team (moved, removed, or the team is gone). Wraps sql.ErrNoRows, so
// callers that treat an unknown key as "not signed in" keep doing so.
var ErrTeamKeyInactive = fmt.Errorf("team key no longer active: %w", sql.ErrNoRows)

// TeamIdentity is what a team credential acts as.
type TeamIdentity struct {
	EventID   string
	EventSlug string
	TeamID    string
	TeamSlug  string // the one site name the credential may act on
	AccountID string // the event's holding account (the site's owner)
	MemberID  string // the person whose credential it is
}

// ResolveTeamIdentity checks that memberID is a participant on teamID right
// now and returns what their team credential acts as. ErrTeamKeyInactive when
// not.
func ResolveTeamIdentity(ctx context.Context, q Querier, memberID, teamID string) (TeamIdentity, error) {
	var t TeamIdentity
	err := q.QueryRowContext(ctx, `
		SELECT e.id, e.slug, t.id, t.slug, e.account_id, m.user_id
		  FROM event_teams t
		  JOIN events e ON e.id = t.event_id
		  JOIN event_members m ON m.event_id = e.id AND m.team_id = t.id
		 WHERE t.id::text = $1 AND m.user_id::text = $2 AND m.role = 'participant'`,
		teamID, memberID).Scan(&t.EventID, &t.EventSlug, &t.TeamID, &t.TeamSlug, &t.AccountID, &t.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrTeamKeyInactive
	}
	return t, err
}

// teamKeyBinding returns the team a stored team key is bound to.
func teamKeyBinding(ctx context.Context, q Querier, keyHash string) (teamID, memberID string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT b.team_id, b.user_id FROM event_team_keys b JOIN api_keys k ON k.id = b.key_id
		 WHERE k.key_hash = $1`, keyHash).Scan(&teamID, &memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrTeamKeyInactive
	}
	return teamID, memberID, err
}

// actAsTeam turns an authenticated member into the holding account acting on
// the team's site: the caller's own identity is kept in Team.MemberID.
func actAsTeam(ctx context.Context, q *sql.DB, member User, id TeamIdentity) (User, error) {
	holder, err := GetUserByID(ctx, q, id.AccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrTeamKeyInactive
		}
		return User{}, err
	}
	holder.IsAdmin = false
	holder.KeyHash = member.KeyHash
	holder.KeyScope = KeyScopeTeam
	holder.KeyExpiresAt = member.KeyExpiresAt
	// The person's own suspension stops their key; the holding account is
	// never suspended by hand (the event's take-down is checked per deploy).
	holder.Suspended = member.Suspended
	holder.SuspendedReason = member.SuspendedReason
	t := id
	holder.Team = &t
	return holder, nil
}

// TeamScopeOfKey answers the scope gate for a team credential: whether apiKey
// is one (a stored key of scope team, or an internal credential bound to a
// team) and, if so, what it acts as right now (live=false when the person is
// no longer on that team: every route is then refused).
func TeamScopeOfKey(ctx context.Context, q Querier, apiKey string) (isTeam, live bool, id TeamIdentity, err error) {
	memberID, teamID, internal := lookupInternalTeamKey(apiKey)
	if !internal {
		var scope string
		err = q.QueryRowContext(ctx, `SELECT scope FROM api_keys WHERE key_hash = $1`, HashAPIKey(apiKey)).Scan(&scope)
		if errors.Is(err, sql.ErrNoRows) {
			return false, false, id, nil
		}
		if err != nil || scope != KeyScopeTeam {
			return false, false, id, err
		}
		teamID, memberID, err = teamKeyBinding(ctx, q, HashAPIKey(apiKey))
		if errors.Is(err, sql.ErrNoRows) {
			return true, false, id, nil
		}
		if err != nil {
			return true, false, id, err
		}
	}
	id, err = ResolveTeamIdentity(ctx, q, memberID, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, false, id, nil
	}
	return true, err == nil, id, err
}

// TeamKeyInfo is a member's team key as the member sees it.
type TeamKeyInfo struct {
	KeyID      string
	Last4      string
	TeamID     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// GetTeamKey returns the person's team key for the event, if any.
func GetTeamKey(ctx context.Context, q Querier, eventID, userID string) (TeamKeyInfo, error) {
	var k TeamKeyInfo
	err := q.QueryRowContext(ctx, `
		SELECT k.id, COALESCE(k.last4, ''), b.team_id, k.created_at, k.last_used_at
		  FROM event_team_keys b JOIN api_keys k ON k.id = b.key_id
		 WHERE b.event_id = $1 AND b.user_id = $2`, eventID, userID).
		Scan(&k.KeyID, &k.Last4, &k.TeamID, &k.CreatedAt, &k.LastUsedAt)
	return k, err
}

// ReplaceTeamKey stores apiKey as the person's one team key for the event
// (their earlier one, if any, stops working in the same transaction).
func ReplaceTeamKey(ctx context.Context, tx *sql.Tx, eventID, teamID, userID, apiKey, name string) (TeamKeyInfo, error) {
	if _, err := RevokeTeamKeys(ctx, tx, eventID, userID); err != nil {
		return TeamKeyInfo{}, err
	}
	var k TeamKeyInfo
	err := tx.QueryRowContext(ctx, `
		INSERT INTO api_keys (key_hash, user_id, name, last4, scope)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, COALESCE(last4, ''), created_at`,
		HashAPIKey(apiKey), userID, name, keyLast4(apiKey), KeyScopeTeam).Scan(&k.KeyID, &k.Last4, &k.CreatedAt)
	if err != nil {
		return TeamKeyInfo{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_team_keys (key_id, event_id, team_id, user_id) VALUES ($1, $2, $3, $4)`,
		k.KeyID, eventID, teamID, userID); err != nil {
		return TeamKeyInfo{}, err
	}
	k.TeamID = teamID
	return k, nil
}

// RevokeTeamKeys deletes the person's team keys for the event ("" userID:
// everyone's). Returns how many.
func RevokeTeamKeys(ctx context.Context, q Querier, eventID, userID string) (int64, error) {
	var res sql.Result
	var err error
	if userID == "" {
		res, err = q.ExecContext(ctx, `
			DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE event_id = $1)`, eventID)
	} else {
		res, err = q.ExecContext(ctx, `
			DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE event_id = $1 AND user_id = $2)`, eventID, userID)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RevokeStaleTeamKeys deletes the event's team keys whose person is no longer
// a participant on the key's team (moved, taken off, removed). They already
// stop working on their own; this keeps the lists honest.
func RevokeStaleTeamKeys(ctx context.Context, q Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `
		DELETE FROM api_keys WHERE id IN (
			SELECT b.key_id FROM event_team_keys b
			  LEFT JOIN event_members m ON m.event_id = b.event_id AND m.user_id = b.user_id
			 WHERE b.event_id = $1 AND (m.user_id IS NULL OR m.role <> 'participant' OR m.team_id IS DISTINCT FROM b.team_id))`, eventID)
	return err
}

// TeamWriteState is what decides whether a team may change its site or entry
// now. Read with TeamWriteStateFor.
type TeamWriteState struct {
	TeamID          string
	EventID         string
	EventSlug       string
	TeamSlug        string
	AccountID       string
	Stage           string
	EventTakenDown  bool
	SiteTakenDown   bool
	Deadline        sql.NullTime // effective: the team's own, else the event's
	Now             time.Time    // the database clock when this was read
	PinnedAt        sql.NullTime
	PinnedVersion   sql.NullInt64
	HasOverride     bool
	EventDeadline   sql.NullTime
	TeamDeadlineSet sql.NullTime
}

// Frozen: the team's deadline has passed.
func (s TeamWriteState) Frozen() bool {
	return s.Deadline.Valid && !s.Now.Before(s.Deadline.Time)
}

// WriteRefusal says why the team may not change its site or entry now:
// "" when it may. Codes: event_taken_down, event_closed (ended),
// team_site_taken_down, submissions_closed.
func (s TeamWriteState) WriteRefusal() string {
	switch {
	case s.EventTakenDown:
		return "event_taken_down"
	case s.Stage == "archived":
		return "event_closed"
	case s.SiteTakenDown:
		return "team_site_taken_down"
	case s.Frozen():
		return "submissions_closed"
	}
	return ""
}

// TeamWriteStateFor reads a team's write state. In a transaction with share
// set, the team row is held FOR SHARE until the transaction ends, so the
// deadline pin (PinDueTeams, FOR UPDATE) waits for a deploy that was let in
// before the deadline and then pins what it made live. The time compared is
// clock_timestamp(), not the transaction's start.
func TeamWriteStateFor(ctx context.Context, q Querier, teamID string, share bool) (TeamWriteState, error) {
	lock := ""
	if share {
		lock = " FOR SHARE OF t"
	}
	var s TeamWriteState
	err := q.QueryRowContext(ctx, `
		SELECT t.id, e.id, e.slug, t.slug, e.account_id, e.stage, e.taken_down_at IS NOT NULL,
		       t.site_taken_down_at IS NOT NULL, COALESCE(t.deadline_override, e.submission_deadline),
		       clock_timestamp(), t.pinned_at, t.pinned_version, t.deadline_override IS NOT NULL,
		       e.submission_deadline, t.deadline_override
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.id::text = $1`+lock, teamID).Scan(
		&s.TeamID, &s.EventID, &s.EventSlug, &s.TeamSlug, &s.AccountID, &s.Stage, &s.EventTakenDown,
		&s.SiteTakenDown, &s.Deadline, &s.Now, &s.PinnedAt, &s.PinnedVersion, &s.HasOverride,
		&s.EventDeadline, &s.TeamDeadlineSet)
	return s, err
}

// PinDueTeams pins every team whose deadline has passed and that is not
// pinned yet: pinned_version is its site's live version at that moment (NULL
// when it has no live site). eventID "" means every event. Each team is
// pinned in its own transaction holding the team row FOR UPDATE, so a deploy
// already let in (holding it FOR SHARE) finishes first.
func PinDueTeams(ctx context.Context, database *sql.DB, eventID string) error {
	rows, err := database.QueryContext(ctx, `
		SELECT t.id FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.pinned_at IS NULL
		   AND COALESCE(t.deadline_override, e.submission_deadline) <= clock_timestamp()
		   AND ($1 = '' OR e.id::text = $1)`, eventID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := pinTeam(ctx, database, id); err != nil {
			return err
		}
	}
	return nil
}

func pinTeam(ctx context.Context, database *sql.DB, teamID string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var (
		due       bool
		slug      string
		accountID string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT t.pinned_at IS NULL AND COALESCE(t.deadline_override, e.submission_deadline) <= clock_timestamp(),
		       t.slug, e.account_id
		  FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE t.id = $1
		   FOR UPDATE OF t`, teamID).Scan(&due, &slug, &accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	var v sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT active_version FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL`,
		accountID, slug).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE event_teams SET pinned_version = $2, pinned_at = clock_timestamp() WHERE id = $1`, teamID, v); err != nil {
		return err
	}
	return tx.Commit()
}

// UnpinReopened clears the pin of every team of the event whose deadline is
// no longer passed (the organiser moved it). Call in the transaction that
// moved it.
func UnpinReopened(ctx context.Context, q Querier, eventID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_teams t SET pinned_version = NULL, pinned_at = NULL
		  FROM events e
		 WHERE e.id = t.event_id AND e.id = $1 AND t.pinned_at IS NOT NULL
		   AND (COALESCE(t.deadline_override, e.submission_deadline) IS NULL
		        OR COALESCE(t.deadline_override, e.submission_deadline) > clock_timestamp())`, eventID)
	return err
}

// PinnedVersions returns the versions pinned for the holding account's team
// sites, by site name (for keeping them when old versions are pruned).
func PinnedVersionOfSite(ctx context.Context, q Querier, accountID, siteName string) (int, bool, error) {
	var v sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT t.pinned_version FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.account_id = $1 AND t.slug = $2 AND t.pinned_version IS NOT NULL`, accountID, siteName).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return int(v.Int64), v.Valid, nil
}

// HoldingAccountEvent returns the event held by accountID, if it is one.
func HoldingAccountEvent(ctx context.Context, q Querier, accountID string) (Event, bool, error) {
	ev, err := GetEventByAccount(ctx, q, accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	return ev, err == nil, err
}

// TeamSiteNameUsed reports whether the holding account has, or had, a site
// by that name (live, in Recently deleted, or as a renamed site's old name):
// a new team never gets a name whose site belonged to another team.
func TeamSiteNameUsed(ctx context.Context, q Querier, accountID, name string) (bool, error) {
	var used bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM sites WHERE user_id = $1 AND lower(name) = lower($2))`,
		accountID, name).Scan(&used)
	return used, err
}

// OrphanTeamSites lists live sites of events' holding accounts whose team no
// longer exists (the team was removed or emptied): the sweep takes them to
// Recently deleted.
func OrphanTeamSites(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}) ([]Site, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.active_version
		  FROM sites s JOIN events e ON e.account_id = s.user_id
		 WHERE s.deleted_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM event_teams t WHERE t.event_id = e.id AND t.slug = s.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.ActiveVersion); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
