package gate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/web"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// StartSSOHop starts the company sign-in's hop through the apex for site
// siteID on host, returning to returnTo: a fresh state (the hop's name on
// the apex) and nonce (the browser's proof, kept in its cookie; only its
// hash is stored).
func StartSSOHop(ctx context.Context, db *sql.DB, siteID, host, returnTo string) (state, nonce string, err error) {
	if nonce, err = accounts.SecureHex(32); err != nil {
		return "", "", err
	}
	if state, err = accounts.SecureHex(32); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(nonce))
	hop := model.SSOHop{State: state, SiteID: siteID, Host: host, ReturnTo: returnTo, NonceHash: base64.RawURLEncoding.EncodeToString(sum[:])}
	if err := repo.InsertSSOHop(ctx, db, hop); err != nil {
		return "", "", err
	}
	return state, nonce, nil
}

// ErrSSOPassNotIssued: no apex pass could be issued for the key.
var ErrSSOPassNotIssued = errors.New("sso pass not issued")

// BootstrapSSOPass keeps the apex sign-in pass of account key keyHash for
// this browser, whose current pass cookie hashes to cookieHash: that pass
// touched for idle when it is this key's, else a fresh pass (ttl, idle,
// keyIdle) replacing it. raw is the fresh pass's cookie value, "" when the
// current one stays. ErrSSOPassNotIssued when no fresh pass could be made.
func BootstrapSSOPass(ctx context.Context, db *sql.DB, keyHash, cookieHash string, ttl, idle, keyIdle time.Duration) (raw string, err error) {
	if p, err := GetSSOPass(ctx, db, cookieHash, keyIdle); err == nil && p.ParentKeyHash == keyHash {
		return "", TouchSSOPass(ctx, db, p.Hash, idle)
	}
	if raw, err = accounts.SecureHex(32); err != nil {
		return "", ErrSSOPassNotIssued
	}
	if err := accounts.New(db).ReplaceSSOPass(ctx, accounts.HashSecret(raw), keyHash, cookieHash, ttl, idle, keyIdle); err != nil {
		return "", ErrSSOPassNotIssued
	}
	return raw, nil
}

// ErrSSOSignoutUnknown: no live sign-out ticket has that token.
var ErrSSOSignoutUnknown = errors.New("sso sign-out ticket unknown")

// ConfirmSSOSignout is the apex sign-out ticket token, for the browser
// whose pass cookie hashes to cookieHash: confirmed when the ticket is that
// browser's own pass; otherwise a fresh one-time confirmation form token
// (form), bound to the ticket and this browser, and returnTo, where Cancel
// goes. ErrSSOSignoutUnknown for no such ticket.
func ConfirmSSOSignout(ctx context.Context, db *sql.DB, token, cookieHash string) (confirmed bool, form, returnTo string, err error) {
	ticket, err := GetSSOSignout(ctx, db, token)
	if err != nil {
		return false, "", "", ErrSSOSignoutUnknown
	}
	if ticket.PassHash != "" && ticket.PassHash == cookieHash {
		return true, "", "", nil
	}
	if form, err = accounts.SecureHex(32); err != nil {
		return false, "", "", err
	}
	if err := SetSSOSignoutForm(ctx, db, token, accounts.HashSecret(form), cookieHash); err != nil {
		return false, "", "", err
	}
	return false, form, ticket.ReturnTo, nil
}

func GetSSOPass(ctx context.Context, db *sql.DB, hash string, keyIdle time.Duration) (model.SSOPass, error) {
	return repo.GetSSOPass(ctx, db, hash, keyIdle)
}

func TouchSSOPass(ctx context.Context, db *sql.DB, hash string, idle time.Duration) error {
	return repo.TouchSSOPass(ctx, db, hash, idle)
}

func InsertSSOHop(ctx context.Context, db *sql.DB, h model.SSOHop) error {
	return repo.InsertSSOHop(ctx, db, h)
}

func SetSSOConsentForm(ctx context.Context, db *sql.DB, state, hash, pass string) error {
	return repo.SetSSOConsentForm(ctx, db, state, hash, pass)
}

func HasSSOConsent(ctx context.Context, db *sql.DB, userID, siteID string) (bool, error) {
	return repo.HasSSOConsent(ctx, db, userID, siteID)
}

func GetSSOSignout(ctx context.Context, db *sql.DB, token string) (model.SSOSignout, error) {
	return repo.GetSSOSignout(ctx, db, token)
}

func SetSSOSignoutForm(ctx context.Context, db *sql.DB, token, formHash, cookieHash string) error {
	return repo.SetSSOSignoutForm(ctx, db, token, formHash, cookieHash)
}

func ForgetSSOConsent(ctx context.Context, db *sql.DB, userID, siteID string) error {
	return repo.ForgetSSOConsent(ctx, db, userID, siteID)
}

func RedeemSSOHop(ctx context.Context, db *sql.DB, state string, pass model.SSOPass, post bool, formToken string, keyIdle, sessionIdle time.Duration) (accounts.SSOHandoff, error) {
	fail := func(status int, reason string, err error) (accounts.SSOHandoff, error) {
		return accounts.SSOHandoff{Status: status, Failure: reason}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail(500, "", err)
	}
	defer tx.Rollback()
	hop, err := repo.GetSSOHop(ctx, tx, state, true)
	if err != nil {
		return fail(400, "state_used", err)
	}
	if _, err = repo.GetSSOPass(ctx, tx, pass.Hash, keyIdle); err != nil {
		return fail(401, "pass_gone", err)
	}
	if post {
		if hop.ConsentPassHash.String != pass.Hash || !web.NonceMatches(formToken, hop.ConsentHash.String) {
			return fail(403, "", errors.New("consent mismatch"))
		}
		if err = repo.RememberSSOConsent(ctx, tx, pass.UserID, hop.SiteID); err != nil {
			return fail(500, "", err)
		}
	}
	if err := repo.LimitPendingSSOSessions(ctx, tx, pass.Hash, hop.SiteID); err != nil {
		return fail(429, "too_many_pending", err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		return fail(500, "", err)
	}
	once, err := accounts.SecureHex(32)
	if err != nil {
		return fail(500, "", err)
	}
	now := time.Now()
	if err = repo.InsertPendingVisitorSession(ctx, tx, id, pass.UserID, hop.SiteID, hop.Host, now.Add(time.Minute)); err == nil {
		err = repo.ParentVisitorSession(ctx, tx, id, pass.Hash)
	}
	if err == nil {
		err = repo.InsertEstablishToken(ctx, tx, once, id, hop.Host, hop.ReturnTo, sql.NullString{String: hop.NonceHash, Valid: true}, now.Add(time.Minute))
	}
	if err == nil {
		err = repo.TouchSSOPass(ctx, tx, pass.Hash, sessionIdle)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fail(500, "mint", err)
	}
	return accounts.SSOHandoff{Hop: hop, Once: once}, nil
}

// SettleEstablish ends a claimed sign-in (ClaimEstablish): admit activates
// the pending session id for ttl and idle (first: the visitor's first
// session on the site); otherwise, or when activation fails, it is deleted.
// No id (the code was never spent) settles nothing.
func SettleEstablish(ctx context.Context, db *sql.DB, id []byte, admit bool, ttl, idle time.Duration) (first bool, err error) {
	if len(id) == 0 {
		return false, nil
	}
	if admit {
		if first, err = repo.ActivateVisitorSession(ctx, db, id, ttl, idle); err == nil {
			return first, nil
		}
	}
	_ = repo.DeleteVisitorSession(ctx, db, id)
	return false, err
}

// StartVisitorSession signs userID in to siteID on host for ttl, sliding
// idle. A suspended account gets no session (model.ErrAccountSuspended).
func StartVisitorSession(ctx context.Context, db *sql.DB, userID, siteID, host string, ttl, idle time.Duration) (accounts.VisitorSessionStart, error) {
	susp, err := repo.UserSuspended(ctx, db, userID)
	if err != nil {
		return accounts.VisitorSessionStart{}, err
	}
	if susp {
		return accounts.VisitorSessionStart{}, model.ErrAccountSuspended
	}
	id, err := ProviderRandomRaw(32)
	if err != nil {
		return accounts.VisitorSessionStart{}, err
	}
	now := time.Now()
	start := accounts.VisitorSessionStart{ID: id, IdleExpiresAt: now.Add(idle)}
	start.First, err = repo.InsertVisitorSession(ctx, db, id, userID, siteID, host, now.Add(ttl), start.IdleExpiresAt)
	return start, err
}
