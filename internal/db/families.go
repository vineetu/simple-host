package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Address families (owner decision 2026-09-29; handler/familyhost.go).
//
// An account connects *.<suffix> once; every site of that account named
// <SitePrefix><label> then also answers at <label>.<suffix>. A family is
// exclusive across accounts only once it is verified: before that it serves
// nothing, and several accounts may wait on the same name; the first to
// prove it wins and the others are let go.

// AddressFamily is one row of address_families.
type AddressFamily struct {
	ID              string
	UserID          string
	Suffix          string
	SitePrefix      string
	Rank            int
	Canonical       bool
	Token           string
	Status          string // pending | active | failing
	LastError       string
	BoundAt         time.Time
	VerifiedAt      sql.NullTime
	CheckedAt       sql.NullTime
	FailingSince    sql.NullTime
	LapseNotifiedAt sql.NullTime
	ReleaseAt       sql.NullTime
	CertMode        string // wildcard | per_host
	CertName        string
	ProofExempt     bool
	CreatedAt       time.Time
	// Handle and Email are the owner's (filled by every read here).
	Handle string
	Email  string
}

// Verified reports whether the family has proved itself (it may be failing
// its checks since; it keeps serving until it lapses).
func (f AddressFamily) Verified() bool { return f.VerifiedAt.Valid }

const familyColumns = `f.id, f.user_id, f.suffix, f.site_prefix, f.rank, f.canonical, f.token, f.status,
	COALESCE(f.last_error, ''), f.bound_at, f.verified_at, f.checked_at, f.failing_since, f.lapse_notified_at,
	f.release_at, f.cert_mode, COALESCE(f.cert_name, ''), f.proof_exempt, f.created_at,
	COALESCE(u.handle, ''), u.username`

const familyFrom = ` FROM address_families f JOIN users u ON u.id = f.user_id `

// familyQuerier is a Querier that can also list rows (*sql.DB and *sql.Tx).
type familyQuerier interface {
	Querier
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func scanFamily(row interface{ Scan(...any) error }) (AddressFamily, error) {
	var f AddressFamily
	err := row.Scan(&f.ID, &f.UserID, &f.Suffix, &f.SitePrefix, &f.Rank, &f.Canonical, &f.Token, &f.Status,
		&f.LastError, &f.BoundAt, &f.VerifiedAt, &f.CheckedAt, &f.FailingSince, &f.LapseNotifiedAt,
		&f.ReleaseAt, &f.CertMode, &f.CertName, &f.ProofExempt, &f.CreatedAt,
		&f.Handle, &f.Email)
	return f, err
}

func queryFamilies(ctx context.Context, q familyQuerier, where string, args ...any) ([]AddressFamily, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+familyColumns+familyFrom+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AddressFamily
	for rows.Next() {
		f, err := scanFamily(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListFamiliesByUser is the account's families, most specific first.
func ListFamiliesByUser(ctx context.Context, q familyQuerier, userID string) ([]AddressFamily, error) {
	return queryFamilies(ctx, q, `WHERE f.user_id = $1 ORDER BY f.suffix`, userID)
}

// ListAllFamilies is every family on the instance (admin view).
func ListAllFamilies(ctx context.Context, q familyQuerier) ([]AddressFamily, error) {
	return queryFamilies(ctx, q, `ORDER BY f.suffix, f.created_at`)
}

// ListVerifiedFamilies is every family that has proved itself: the ones that
// may serve (the handler adds the certificate check).
func ListVerifiedFamilies(ctx context.Context, q familyQuerier) ([]AddressFamily, error) {
	return queryFamilies(ctx, q, `WHERE f.verified_at IS NOT NULL ORDER BY f.suffix`)
}

// GetFamilyByUserSuffix is one account's family. sql.ErrNoRows if none.
func GetFamilyByUserSuffix(ctx context.Context, q Querier, userID, suffix string) (AddressFamily, error) {
	return scanFamily(q.QueryRowContext(ctx, `SELECT `+familyColumns+familyFrom+`WHERE f.user_id = $1 AND f.suffix = $2`, userID, suffix))
}

// GetFamilyByID is one family by id. sql.ErrNoRows if none.
func GetFamilyByID(ctx context.Context, q Querier, id string) (AddressFamily, error) {
	return scanFamily(q.QueryRowContext(ctx, `SELECT `+familyColumns+familyFrom+`WHERE f.id::text = $1`, id))
}

// ListFamiliesToCheck is every pending family, plus verified ones whose last
// check is older than recheck.
func ListFamiliesToCheck(ctx context.Context, q familyQuerier, recheck time.Duration) ([]AddressFamily, error) {
	return queryFamilies(ctx, q, `WHERE f.verified_at IS NULL OR f.checked_at IS NULL
		OR f.checked_at < now() - ($1 * interval '1 second') OR f.status <> 'active'
		ORDER BY f.suffix`, int64(recheck.Seconds()))
}

var (
	// ErrFamilyTaken: another account holds the name (a verified family
	// that overlaps it, or a custom domain under it).
	ErrFamilyTaken = errors.New("that name is connected to another account")
	// ErrFamilyExists: this account already has this family.
	ErrFamilyExists = errors.New("this family is already connected")
	// ErrFamilyCap: the account has as many families as it may.
	ErrFamilyCap = errors.New("too many address families")
)

// FamilyLockKey is the advisory-lock key that serializes every bind of a
// family and of a custom domain under the same registrable domain.
func FamilyLockKey(name string) string {
	reg, err := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(strings.TrimSuffix(name, ".")))
	if err != nil {
		reg = strings.ToLower(name)
	}
	return "family:" + reg
}

func lockFamilyName(ctx context.Context, tx *sql.Tx, name string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, FamilyLockKey(name))
	return err
}

// overlaps is SQL true when the suffixes $a and column c are the same name
// or one is under the other.
func overlapsSQL(a, c string) string {
	return `(` + c + ` = ` + a + ` OR right(` + a + `, length(` + c + `) + 1) = '.' || ` + c +
		` OR right(` + c + `, length(` + a + `) + 1) = '.' || ` + a + `)`
}

// familyConflict reports whether another account holds suffix: a verified
// family of theirs overlaps it, or a site of theirs has (or still serves at)
// a custom domain equal to it or under it.
func familyConflict(ctx context.Context, q Querier, userID, suffix string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM address_families f WHERE f.user_id <> $1 AND f.verified_at IS NOT NULL AND `+overlapsSQL("$2", "f.suffix")+`)
		OR EXISTS (SELECT 1 FROM sites s WHERE s.user_id <> $1 AND (
			lower(s.custom_domain) = $2 OR right(lower(s.custom_domain), length($2) + 1) = '.' || $2
			OR lower(s.previous_domain) = $2 OR right(lower(s.previous_domain), length($2) + 1) = '.' || $2))`,
		userID, suffix).Scan(&taken)
	return taken, err
}

// FamilyBind is what an account asks for when it connects a family.
type FamilyBind struct {
	UserID      string
	Suffix      string
	SitePrefix  string
	Rank        int
	Canonical   bool
	CertMode    string
	CertName    string
	ProofExempt bool
	// Max is the account's cap (ADDRESS_FAMILIES_PER_ACCOUNT); 0 = none allowed.
	Max int
}

// BindFamily adds a pending family for an account. Refused when another
// account holds the name (ErrFamilyTaken), when this account already has it
// (ErrFamilyExists) or when the account is at its cap (ErrFamilyCap). Runs
// under the lock that custom-domain binds under the same registrable domain
// also take, so the two can never race past each other's check.
func BindFamily(ctx context.Context, database *sql.DB, b FamilyBind) (AddressFamily, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return AddressFamily{}, err
	}
	defer tx.Rollback()
	if err := lockFamilyName(ctx, tx, b.Suffix); err != nil {
		return AddressFamily{}, err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('family-user:' || $1, 0))`, b.UserID); err != nil {
		return AddressFamily{}, err
	}
	var exists bool
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(bool_or(suffix = $2), false), count(*) FROM address_families WHERE user_id = $1`, b.UserID, b.Suffix).Scan(&exists, &n); err != nil {
		return AddressFamily{}, err
	}
	if exists {
		return AddressFamily{}, ErrFamilyExists
	}
	if n >= b.Max {
		return AddressFamily{}, ErrFamilyCap
	}
	taken, err := familyConflict(ctx, tx, b.UserID, b.Suffix)
	if err != nil {
		return AddressFamily{}, err
	}
	if taken {
		return AddressFamily{}, ErrFamilyTaken
	}
	mode := b.CertMode
	if mode == "" {
		mode = "wildcard"
	}
	var id string
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO address_families (user_id, suffix, site_prefix, rank, canonical, cert_mode, cert_name, proof_exempt)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), $8) RETURNING id`,
		b.UserID, b.Suffix, b.SitePrefix, b.Rank, b.Canonical, mode, b.CertName, b.ProofExempt).Scan(&id); err != nil {
		return AddressFamily{}, err
	}
	f, err := GetFamilyByID(ctx, tx, id)
	if err != nil {
		return AddressFamily{}, err
	}
	return f, tx.Commit()
}

// FamilyPatch changes a family's settings; nil leaves one as it is.
type FamilyPatch struct {
	SitePrefix *string
	Rank       *int
	Canonical  *bool
}

// UpdateFamily applies p to the account's family and returns it.
func UpdateFamily(ctx context.Context, q Querier, userID, suffix string, p FamilyPatch) (AddressFamily, error) {
	res, err := q.ExecContext(ctx, `UPDATE address_families SET
		site_prefix = COALESCE($3, site_prefix), rank = COALESCE($4, rank), canonical = COALESCE($5, canonical)
		WHERE user_id = $1 AND suffix = $2`, userID, suffix, p.SitePrefix, p.Rank, p.Canonical)
	if err != nil {
		return AddressFamily{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return AddressFamily{}, sql.ErrNoRows
	}
	return GetFamilyByUserSuffix(ctx, q, userID, suffix)
}

// DeleteFamily removes a family (by id) and returns what it was.
func DeleteFamily(ctx context.Context, q Querier, id string) (AddressFamily, error) {
	f, err := GetFamilyByID(ctx, q, id)
	if err != nil {
		return AddressFamily{}, err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM address_families WHERE id = $1`, f.ID); err != nil {
		return AddressFamily{}, err
	}
	return f, nil
}

// VerifyFamily records that a pending family proved itself. It wins only when
// no other account has verified an overlapping family and no other account's
// site has a custom domain under it meanwhile (won=false, nothing changed).
// Every other account's pending family on an overlapping name is let go and
// returned, so their owners can be told.
func VerifyFamily(ctx context.Context, database *sql.DB, id string) (won bool, losers []AddressFamily, err error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	f, err := GetFamilyByID(ctx, tx, id)
	if err != nil {
		return false, nil, err
	}
	if err := lockFamilyName(ctx, tx, f.Suffix); err != nil {
		return false, nil, err
	}
	if f.VerifiedAt.Valid {
		return true, nil, nil
	}
	taken, err := familyConflict(ctx, tx, f.UserID, f.Suffix)
	if err != nil {
		return false, nil, err
	}
	if taken {
		return false, nil, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE address_families SET verified_at = now(), checked_at = now(), status = 'active',
		last_error = NULL, failing_since = NULL, lapse_notified_at = NULL, release_at = NULL WHERE id = $1`, f.ID); err != nil {
		return false, nil, err
	}
	losers, err = queryFamilies(ctx, tx, `WHERE f.user_id <> $1 AND f.verified_at IS NULL AND `+overlapsSQL("$2", "f.suffix"), f.UserID, f.Suffix)
	if err != nil {
		return false, nil, err
	}
	for _, l := range losers {
		if _, err := tx.ExecContext(ctx, `DELETE FROM address_families WHERE id = $1`, l.ID); err != nil {
			return false, nil, err
		}
	}
	return true, losers, tx.Commit()
}

// FamilyCheck is what SetFamilyCheck reports about a verified family that is
// failing: since when, and whether (and until when) its owner was told.
type FamilyCheck struct {
	FailingSince sql.NullTime
	Notified     bool
	ReleaseAt    sql.NullTime
}

// SetFamilyCheck records one check: ok clears any failure; otherwise a
// verified family starts (or keeps) its failing clock and a pending one keeps
// waiting with the reason.
func SetFamilyCheck(ctx context.Context, q Querier, id string, ok bool, reason string) (FamilyCheck, error) {
	var c FamilyCheck
	err := q.QueryRowContext(ctx, `
		UPDATE address_families SET checked_at = now(),
		    last_error = CASE WHEN $2 THEN NULL ELSE NULLIF($3, '') END,
		    status = CASE WHEN verified_at IS NULL THEN 'pending' WHEN $2 THEN 'active' ELSE 'failing' END,
		    failing_since = CASE WHEN $2 OR verified_at IS NULL THEN NULL ELSE COALESCE(failing_since, now()) END,
		    lapse_notified_at = CASE WHEN $2 THEN NULL ELSE lapse_notified_at END,
		    release_at = CASE WHEN $2 THEN NULL ELSE release_at END
		WHERE id = $1
		RETURNING failing_since, lapse_notified_at IS NOT NULL, release_at`, id, ok, reason).Scan(&c.FailingSince, &c.Notified, &c.ReleaseAt)
	if errors.Is(err, sql.ErrNoRows) {
		return FamilyCheck{}, nil
	}
	return c, err
}

// MarkFamilyLapseNotified records that the owner was told their failing
// family will be let go at releaseAt.
func MarkFamilyLapseNotified(ctx context.Context, q Querier, id string, releaseAt time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE address_families SET lapse_notified_at = now(), release_at = $2 WHERE id = $1`, id, releaseAt)
	return err
}

// ReleaseExpiredFamilies removes pending families older than ttl and
// returns them.
func ReleaseExpiredFamilies(ctx context.Context, q familyQuerier, ttl time.Duration) ([]AddressFamily, error) {
	expired, err := queryFamilies(ctx, q, `WHERE f.verified_at IS NULL AND f.bound_at < now() - ($1 * interval '1 second')`, int64(ttl.Seconds()))
	if err != nil {
		return nil, err
	}
	for _, f := range expired {
		if _, err := q.ExecContext(ctx, `DELETE FROM address_families WHERE id = $1 AND verified_at IS NULL`, f.ID); err != nil {
			return nil, err
		}
	}
	return expired, nil
}

// SetFamilyCert sets the family's certificate mode and the operator's
// lineage name (admin only).
func SetFamilyCert(ctx context.Context, q Querier, id, mode, certName string) error {
	res, err := q.ExecContext(ctx, `UPDATE address_families SET cert_mode = $2, cert_name = NULLIF($3, '') WHERE id::text = $1`, id, mode, certName)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetFamilyProofExempt turns the admin's "no TXT record needed" flag on or off.
func SetFamilyProofExempt(ctx context.Context, q Querier, id string, on bool) error {
	res, err := q.ExecContext(ctx, `UPDATE address_families SET proof_exempt = $2 WHERE id::text = $1`, id, on)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DomainUnderOtherFamily reports whether domain is (or is under) a verified
// family of an account other than userID: such a name is that account's.
func DomainUnderOtherFamily(ctx context.Context, q Querier, domain, userID string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM address_families f
		WHERE f.user_id::text <> $2 AND f.verified_at IS NOT NULL
		  AND (lower($1) = f.suffix OR right(lower($1), length(f.suffix) + 1) = '.' || f.suffix))`, domain, userID).Scan(&taken)
	return taken, err
}
