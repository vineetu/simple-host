package db

import (
	"context"
	"database/sql"
	"time"
)

type SSOPass struct{ Hash, UserID, ParentKeyHash string }
type SSOHop struct {
	State, SiteID, Host, ReturnTo, NonceHash string
	ConsentHash, ConsentPassHash             sql.NullString
}

// The live parent predicate is shared by pass issuance, redemption, and site
// session reads. Expiry and suspension take effect without a cleanup worker.
const liveSSOParent = `k.scope = 'full' AND u.suspended_at IS NULL
 AND (k.expires_at IS NULL OR k.expires_at > now())
 AND ($2::float8 <= 0 OR GREATEST(k.idle_from, COALESCE(k.last_used_at, k.idle_from)) + make_interval(secs => $2::float8) > now())`
const liveSSOPass = `p.expires_at > now() AND p.idle_expires_at > now() AND ` + liveSSOParent

// ReplaceSSOPass serializes issuance on the parent key, retaining at most one
// pass per key. Replacing a browser's previous pass retires that key's family.
func ReplaceSSOPass(ctx context.Context, database *sql.DB, hash, keyHash, oldHash string, ttl, idle time.Duration) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT k.key_hash FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key_hash=$1 AND `+liveSSOParent+` FOR UPDATE OF k`, keyHash, idleExpirySeconds()).Scan(&id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE key_hash IN (SELECT parent_key_hash FROM sso_passes WHERE id_hash=$1) AND key_hash<>$2`, oldHash, keyHash); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM sso_passes WHERE parent_key_hash=$1`, keyHash); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO sso_passes(id_hash,user_id,parent_key_hash,expires_at,idle_expires_at)
 SELECT $3,k.user_id,k.key_hash,LEAST(now()+make_interval(secs=>$4),k.expires_at), LEAST(now()+make_interval(secs=>$5),k.expires_at)
 FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key_hash=$1 AND `+liveSSOParent+` RETURNING id_hash`, keyHash, idleExpirySeconds(), hash, ttl.Seconds(), idle.Seconds()).Scan(&id); err != nil {
		return err
	}
	return tx.Commit()
}
func GetSSOPass(ctx context.Context, q Querier, hash string) (SSOPass, error) {
	var p SSOPass
	err := q.QueryRowContext(ctx, `SELECT p.id_hash,p.user_id::text,p.parent_key_hash FROM sso_passes p JOIN api_keys k ON k.key_hash=p.parent_key_hash JOIN users u ON u.id=p.user_id WHERE p.id_hash=$1 AND `+liveSSOPass, hash, idleExpirySeconds()).Scan(&p.Hash, &p.UserID, &p.ParentKeyHash)
	return p, err
}
func TouchSSOPass(ctx context.Context, q Querier, hash string, idle time.Duration) error {
	_, err := q.ExecContext(ctx, `UPDATE sso_passes SET idle_expires_at=LEAST(expires_at,now()+make_interval(secs=>$2)) WHERE id_hash=$1 AND idle_expires_at>now()`, hash, idle.Seconds())
	return err
}
func InsertSSOHop(ctx context.Context, q Querier, h SSOHop) error {
	_, err := q.ExecContext(ctx, `INSERT INTO sso_hops(state,site_id,host,return_to,nonce_hash,expires_at) VALUES($1,$2,$3,$4,$5,now()+interval '10 minutes')`, h.State, h.SiteID, h.Host, h.ReturnTo, h.NonceHash)
	return err
}
func GetSSOHop(ctx context.Context, q Querier, state string, consume bool) (SSOHop, error) {
	query := `SELECT state,site_id::text,host,return_to,nonce_hash,consent_hash,consent_pass_hash FROM sso_hops WHERE state=$1 AND expires_at>now()`
	if consume {
		query = `DELETE FROM sso_hops WHERE state=$1 AND expires_at>now() RETURNING state,site_id::text,host,return_to,nonce_hash,consent_hash,consent_pass_hash`
	}
	var h SSOHop
	err := q.QueryRowContext(ctx, query, state).Scan(&h.State, &h.SiteID, &h.Host, &h.ReturnTo, &h.NonceHash, &h.ConsentHash, &h.ConsentPassHash)
	return h, err
}
func SetSSOConsentForm(ctx context.Context, q Querier, state, hash, pass string) error {
	_, err := q.ExecContext(ctx, `UPDATE sso_hops SET consent_hash=$2,consent_pass_hash=$3 WHERE state=$1`, state, hash, pass)
	return err
}
func HasSSOConsent(ctx context.Context, q Querier, userID, siteID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sso_consents WHERE user_id=$1 AND site_id=$2)`, userID, siteID).Scan(&ok)
	return ok, err
}
func RememberSSOConsent(ctx context.Context, q Querier, userID, siteID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO sso_consents(user_id,site_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, userID, siteID)
	return err
}
func ParentVisitorSession(ctx context.Context, q Querier, id []byte, pass string) error {
	_, err := q.ExecContext(ctx, `UPDATE visitor_sessions SET sso_pass_hash=$2 WHERE id=$1`, id, pass)
	return err
}

// RevokeVisitorSSO deletes the dashboard key, which cascades to all sibling
// sessions. The ticket binds the live site session's parent hash and validated return address.
func RevokeVisitorSSO(ctx context.Context, database *sql.DB, s VisitorSession, token, returnTo string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO sso_signouts(token,pass_hash,return_to,expires_at) VALUES($1,$2,$3,now()+interval '2 minutes')`, token, s.SSOPassHash.String, returnTo); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE key_hash IN (SELECT parent_key_hash FROM sso_passes WHERE id_hash=$1)`, s.SSOPassHash.String); err != nil {
		return err
	}
	return tx.Commit()
}

type SSOSignout struct{ PassHash, ReturnTo string }

func GetSSOSignout(ctx context.Context, q Querier, token string) (SSOSignout, error) {
	var s SSOSignout
	err := q.QueryRowContext(ctx, `SELECT pass_hash,return_to FROM sso_signouts WHERE token=$1 AND expires_at>now()`, token).Scan(&s.PassHash, &s.ReturnTo)
	return s, err
}
func SetSSOSignoutForm(ctx context.Context, q Querier, token, formHash, cookieHash string) error {
	_, err := q.ExecContext(ctx, `UPDATE sso_signouts SET form_hash=$2,form_cookie_hash=$3 WHERE token=$1 AND expires_at>now()`, token, formHash, cookieHash)
	return err
}

// CompleteSSOSignout consumes only a family-bound ticket or a same-origin
// confirmation bound to the cookie that saw the form. The predicate is atomic
// with consumption, so a replay or a changed browser family cannot revoke it.
func CompleteSSOSignout(ctx context.Context, database *sql.DB, token, cookieHash, formHash string) (string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var back string
	if err = tx.QueryRowContext(ctx, `DELETE FROM sso_signouts WHERE token=$1 AND expires_at>now()
 AND (($3='' AND pass_hash<>'' AND pass_hash=$2) OR ($3<>'' AND form_hash=$3 AND form_cookie_hash=$2)) RETURNING return_to`, token, cookieHash, formHash).Scan(&back); err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE key_hash IN (SELECT parent_key_hash FROM sso_passes WHERE id_hash=$1)`, cookieHash); err != nil {
		return "", err
	}
	return back, tx.Commit()
}
func SweepSSO(ctx context.Context, q Querier) error {
	for _, query := range []string{`DELETE FROM sso_hops WHERE expires_at<now()`, `DELETE FROM sso_signouts WHERE expires_at<now()`, `DELETE FROM sso_passes WHERE expires_at<now() OR idle_expires_at<now()`} {
		if _, err := q.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	return nil
}

func ForgetSSOConsent(ctx context.Context, q Querier, userID, siteID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM sso_consents WHERE user_id=$1 AND site_id::text=$2`, userID, siteID)
	return err
}

// Serialize on the pass so concurrent requests cannot exceed eight pending codes
// per site. Established sessions do not count against this short-lived budget.
func LimitPendingSSOSessions(ctx context.Context, tx *sql.Tx, pass, site string) error {
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT id_hash FROM sso_passes WHERE id_hash=$1 FOR UPDATE`, pass).Scan(&hash); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM visitor_sessions v WHERE sso_pass_hash=$1 AND site_id=$2 AND expires_at>now() AND EXISTS(SELECT 1 FROM visitor_establish_tokens t WHERE t.session_id=v.id AND t.used_at IS NULL AND t.expires_at>now())`, pass, site).Scan(&count); err != nil {
		return err
	}
	if count >= 8 {
		return sql.ErrNoRows
	}
	return nil
}
