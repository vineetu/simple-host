package accounts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	siterules "github.com/vsriram/simple-host/internal/rules/sites"
)

// Querier is the shared database/transaction query interface.
type Querier = sqlkit.Querier

// User is the shared Host account record.
type User = model.User

var (
	platformDomainMu sync.RWMutex
	platformDomain   string
	// platformAlso are the other domains whose one-label names are the same
	// names (SITE_DOMAIN and SITE_BASE_DOMAIN while addresses move between
	// them, docs/history/site-base-domain-move.md). Usually empty.
	platformAlso []string
)

// CreateUser inserts an account. When apiKey is not empty it also becomes one
// of the account's keys; only its SHA-256 is stored (see api_keys).
func CreateUser(ctx context.Context, q Querier, username, apiKey string, isAdmin bool) (User, error) {
	const query = `
		WITH u AS (
			INSERT INTO users (username, is_admin)
			VALUES ($1, $3)
			RETURNING id, username, is_admin, created_at, handle, display_name
		), k AS (
			INSERT INTO api_keys (key_hash, user_id)
			SELECT $2, id FROM u WHERE $2 <> ''
		)
		SELECT id, username, is_admin, created_at, handle, display_name FROM u
	`
	keyHash := ""
	if apiKey != "" {
		keyHash = HashAPIKey(apiKey)
	}

	var user User
	err := q.QueryRowContext(ctx, query, username, keyHash, isAdmin).Scan(
		&user.ID,
		&user.Username,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.Handle,
		&user.DisplayName,
	)
	return user, err
}

// HashAPIKey is how an account key is stored and looked up: hex SHA-256.
// A fast hash is right here because keys are 256 random bits, not passwords.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// GetUserByID loads a users row by primary key, including whether the
// operator has suspended it (callers that authenticate must check Suspended).
func GetUserByID(ctx context.Context, q Querier, id string) (User, error) {
	const query = `
		SELECT id, username, is_admin, created_at, handle, display_name,
		       suspended_at IS NOT NULL, COALESCE(suspended_reason, '')
		FROM users
		WHERE id = $1
	`

	var user User
	err := q.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Username,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.Handle,
		&user.DisplayName,
		&user.Suspended,
		&user.SuspendedReason,
	)
	return user, err
}

// GetUserByUsername preserves the shared accounts lookup.
func GetUserByUsername(ctx context.Context, q Querier, username string) (User, error) {
	const query = `
		SELECT id, username, is_admin, created_at, handle, display_name,
		       suspended_at IS NOT NULL, COALESCE(suspended_reason, '')
		FROM users
		WHERE username = $1
	`

	var user User
	err := q.QueryRowContext(ctx, query, username).Scan(
		&user.ID,
		&user.Username,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.Handle,
		&user.DisplayName,
		&user.Suspended,
		&user.SuspendedReason,
	)
	return user, err
}

// PasswordSignupAccount reports whether id was created by
// CreatePasswordAccount (signup_method='password', signup_source IN
// ('website','admin')) -- self sign-up or admin-created, either way never
// proving it controls whatever address it was given. This excludes the
// REVIEW_ACCOUNT_EMAIL reviewer's own account, which also happens to be
// tagged signup_method='password' (ReviewerAccount, signup_source
// 'reviewer') for an unrelated reason and must keep resolving to itself on
// every later sign-in. Email-code sign-in, OAuth/OIDC sign-in and reviewer
// sign-in all use this to refuse resolving to, linking, or reusing such an
// account under an address it was never proven to control (pre-account
// hijacking).
func PasswordSignupAccount(ctx context.Context, q Querier, id string) (bool, error) {
	var ok bool
	// COALESCE matters: signup_method/signup_source is NULL for most
	// pre-existing accounts, and NULL = 'password' is SQL NULL (not false),
	// which cannot Scan into a bool. NULL correctly means "not one of
	// CreatePasswordAccount's accounts" here, i.e. false.
	err := q.QueryRowContext(ctx, `SELECT COALESCE(signup_method = 'password' AND signup_source IN ('website','admin'), false) FROM users WHERE id=$1`, id).Scan(&ok)
	return ok, err
}

// SelfSignupPasswordAccount reports whether id was created through the
// public password sign-up form (signup_method='password',
// signup_source='website'), as opposed to an admin-created password
// account. Such an account never proved it controls any address -- not even
// one that looks like an email -- the way an admin vouching for one does, or
// an email code or OAuth/OIDC sign-in does. Every email-based authorization
// gate that derives an address from a username (visitorEmail and its
// callers: named viewers, savers, visitor sessions, connector/MCP visitor
// tools) must treat such an account as having no verified email, so a
// self-signup under someone else's address can never pass as that person.
func SelfSignupPasswordAccount(ctx context.Context, q Querier, id string) (bool, error) {
	var ok bool
	// COALESCE matters: signup_method/signup_source is NULL for most
	// pre-existing accounts, and NULL = 'password' is SQL NULL (not false),
	// which cannot Scan into a bool. NULL correctly means "not known to be a
	// self-signup password account" here, i.e. false.
	err := q.QueryRowContext(ctx, `SELECT COALESCE(signup_method = 'password' AND signup_source = 'website', false) FROM users WHERE id=$1`, id).Scan(&ok)
	return ok, err
}

// ClaimHandle atomically sets a user's handle iff still unset. Returns (true,nil) on
// success, (false,nil) if the handle is taken (unique violation) so the caller can try
// the next candidate, (false,err) on other errors.
func ClaimHandle(ctx context.Context, q Querier, userID, handle string) (bool, error) {
	// The handle is also the account's address, <handle>.<SITE_DOMAIN>: it
	// must not land on a name a site has claimed or a retired name-subdomain.
	if CurrentPlatformDomain() != "" {
		switch qq := q.(type) {
		case *sql.Tx:
			ok, err := HandleAvailable(ctx, qq, userID, handle)
			if err != nil || !ok {
				return false, err
			}
		case *sql.DB:
			tx, err := qq.BeginTx(ctx, nil)
			if err != nil {
				return false, err
			}
			defer tx.Rollback()
			ok, err := HandleAvailable(ctx, tx, userID, handle)
			if err != nil || !ok {
				return false, err
			}
			claimed, err := claimHandleIn(ctx, tx, userID, handle)
			if err != nil || !claimed {
				return false, err
			}
			return true, tx.Commit()
		}
	}
	return claimHandleIn(ctx, q, userID, handle)
}

func claimHandleIn(ctx context.Context, q Querier, userID, handle string) (bool, error) {
	const query = `
		UPDATE users
		SET handle = $2, handle_changed_at = now()
		WHERE id = $1 AND handle IS NULL
	`
	// A collision must not abort the signup/bulk transaction before the next candidate.
	tx, inTx := q.(*sql.Tx)
	if inTx {
		if _, err := tx.ExecContext(ctx, "SAVEPOINT claim_handle"); err != nil {
			return false, err
		}
	}
	res, err := q.ExecContext(ctx, query, userID, handle)
	if err != nil {
		if IsUniqueViolation(err) {
			if inTx {
				if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT claim_handle"); rollbackErr != nil {
					return false, rollbackErr
				}
				if _, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT claim_handle"); releaseErr != nil {
					return false, releaseErr
				}
			}
			return false, nil
		}
		return false, err
	}
	if inTx {
		if _, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT claim_handle"); err != nil {
			return false, err
		}
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		// Already had a handle, or user id not found.
		return false, nil
	}
	return true, nil
}

// IsUniqueViolation detects a Postgres unique-constraint violation (SQLSTATE 23505).
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// lib/pq and pgx both surface the SQLSTATE in the error text as "23505".
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(strings.ToLower(msg), "unique")
}

// MarkEventAccount records that an organiser made (or reissued) this account
// for an event (users.event_account): no sign-in alerts, no email change, no
// idle cleanup. Only the admin's routes call it; a key's name never does.
func MarkEventAccount(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET event_account = TRUE WHERE id = $1`, userID)
	return err
}

// SetUserSuspended suspends an account (reason non-empty) or re-enables it
// (reason empty). Keys and connector grants are kept; the checks that read
// users.suspended_at refuse them while it is set. Suspending also ends every
// site sign-in of the account (EndVisitorSessions): nothing re-enables them.
func SetUserSuspended(ctx context.Context, q Querier, userID, reason string) error {
	var res sql.Result
	var err error
	if reason != "" {
		res, err = q.ExecContext(ctx,
			`UPDATE users SET suspended_at = COALESCE(suspended_at, now()), suspended_reason = $2 WHERE id::text = $1`,
			userID, reason)
	} else {
		res, err = q.ExecContext(ctx,
			`UPDATE users SET suspended_at = NULL, suspended_reason = NULL WHERE id::text = $1`, userID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if reason != "" {
		return EndVisitorSessions(ctx, q, userID)
	}
	return nil
}

// UserSuspended reports whether an account is suspended. An unknown id is
// not suspended (callers resolve the account elsewhere).
func UserSuspended(ctx context.Context, q Querier, userID string) (bool, error) {
	var s bool
	err := q.QueryRowContext(ctx,
		`SELECT suspended_at IS NOT NULL FROM users WHERE id::text = $1`, userID).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return s, err
}

// TouchVisitorSession slides idle_expires_at to min(now + idle, expires_at)
// (idle: VISITOR_SESSION_IDLE_DAYS), and refreshes a live parent pass within
// its own absolute expiry. An already idle family is never revived.
func TouchVisitorSession(ctx context.Context, q Querier, id []byte, idle time.Duration) error {
	_, err := q.ExecContext(ctx, `
		WITH touched AS (UPDATE visitor_sessions
		SET last_seen_at = now(),
		    idle_expires_at = LEAST(now() + ($2 * interval '1 second'), expires_at)
		WHERE id = $1
		  AND expires_at > now()
		  AND idle_expires_at > now() RETURNING sso_pass_hash)
		UPDATE sso_passes SET idle_expires_at=LEAST(expires_at, now()+($2 * interval '1 second'))
		WHERE id_hash IN (SELECT sso_pass_hash FROM touched) AND idle_expires_at>now() AND expires_at>now()`, id, int64(idle.Seconds()))
	return err
}

// EndVisitorSessions signs the account out of every site it is signed in to
// as a visitor, its own sites included (where such a session carries the
// owner's powers over saved data). Every "get an intruder out" lever calls
// it: Sign out everywhere, email-change completion and undo, the admin's new
// key, removing a Google or GitHub sign-in, and suspension.
func EndVisitorSessions(ctx context.Context, q Querier, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE user_id = $1`, userID)
	return err
}

// SetPlatformDomain sets SITE_DOMAIN for the namespace checks. Empty (the
// default) disables them: an instance with no domain has no names to share.
func SetPlatformDomain(domain string) {
	SetPlatformDomains(domain)
}

// SetPlatformDomains sets the domain names are handed out under (primary:
// the handle address <handle>.<primary>, the namespace lock key) and every
// other domain whose one-label names are the same names: a name taken under
// one is taken under all, and a lookup of <label>.<any> finds a row stored
// under another.
func SetPlatformDomains(primary string, also ...string) {
	p := strings.ToLower(strings.TrimSpace(primary))
	var rest []string
	for _, d := range also {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && d != p && !ContainsString(rest, d) {
			rest = append(rest, d)
		}
	}
	if p == "" {
		rest = nil
	}
	platformDomainMu.Lock()
	platformDomain, platformAlso = p, rest
	platformDomainMu.Unlock()
}

// ContainsString preserves the shared accounts lookup.
func ContainsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// CurrentPlatformDomain preserves the shared accounts lookup.
func CurrentPlatformDomain() string {
	platformDomainMu.RLock()
	defer platformDomainMu.RUnlock()
	return platformDomain
}

// PlatformDomains is the primary domain, then the others ("" primary: none).
func PlatformDomains() []string {
	platformDomainMu.RLock()
	defer platformDomainMu.RUnlock()
	if platformDomain == "" {
		return nil
	}
	return append([]string{platformDomain}, platformAlso...)
}

// PlatformNameLabel returns the label of a one-label host under any platform
// domain.
func PlatformNameLabel(host string) (string, bool) {
	return siterules.PlatformNameLabel(host, PlatformDomains())
}

// HostForms is host and, for a one-label platform name, the same label under
// every other platform domain: every way one name can be stored. Always two
// entries (the second repeats the first when there is no other form), so
// queries keep one shape.
func HostForms(host string) [2]string {
	host = strings.ToLower(strings.TrimSpace(host))
	forms := [2]string{host, host}
	label, ok := PlatformNameLabel(host)
	if !ok {
		return forms
	}
	for _, d := range PlatformDomains() {
		if f := label + "." + d; f != host {
			forms[1] = f
			break
		}
	}
	return forms
}

// LockKey is the namespace lock key of host: a platform name under its
// primary form, so both forms of one name take the same lock.
func LockKey(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if label, ok := PlatformNameLabel(host); ok {
		return label + "." + CurrentPlatformDomain()
	}
	return host
}

// LockPlatformName takes the namespace lock for host inside tx.
func LockPlatformName(ctx context.Context, tx *sql.Tx, host string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, LockKey(host))
	return err
}

// HandleNameTaken reports whether handle, as an address, is already used by a
// claimed site address, a legacy hostname, another account's alias (or a
// retired one: a deleted account's handles keep user_id NULL and stay taken),
// or another account's live site of that name: an unclaimed <name>.<domain>
// still redirects to the oldest such site (LegacyHostRedirect), and a handle
// there would take those old links over.
// Callers hold the namespace lock.
func HandleNameTaken(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	domain := CurrentPlatformDomain()
	if domain == "" {
		return false, nil
	}
	f := HostForms(strings.ToLower(handle) + "." + domain)
	var taken bool
	err := tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM sites WHERE lower(custom_domain) IN ($1, $4) OR lower(previous_domain) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM legacy_hostnames WHERE lower(hostname) IN ($1, $4))
		    OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $2 AND user_id::text IS DISTINCT FROM $3)
		    OR EXISTS (SELECT 1 FROM sites WHERE name = $2 AND deleted_at IS NULL AND user_id::text IS DISTINCT FROM $3)`,
		f[0], strings.ToLower(handle), userID, f[1]).Scan(&taken)
	return taken, err
}

// HandleAvailable takes the namespace lock for handle inside tx and reports
// whether userID may take it as their handle (the unique index on users.handle
// still decides between two accounts).
func HandleAvailable(ctx context.Context, tx *sql.Tx, userID, handle string) (bool, error) {
	domain := CurrentPlatformDomain()
	if domain == "" {
		return true, nil
	}
	if err := LockPlatformName(ctx, tx, strings.ToLower(handle)+"."+domain); err != nil {
		return false, err
	}
	taken, err := HandleNameTaken(ctx, tx, userID, handle)
	return !taken, err
}
