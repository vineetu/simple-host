package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/accounts"
	"time"
)

func (s *Service) GetSSOPass(ctx context.Context, hash string, keyIdle time.Duration) (model.SSOPass, error) {
	return repo.GetSSOPass(ctx, s.database, hash, keyIdle)
}
func (s *Service) TouchSSOPass(ctx context.Context, hash string, idle time.Duration) error {
	return repo.TouchSSOPass(ctx, s.database, hash, idle)
}
func (s *Service) InsertSSOHop(ctx context.Context, h model.SSOHop) error {
	return repo.InsertSSOHop(ctx, s.database, h)
}
func (s *Service) SetSSOConsentForm(ctx context.Context, state, hash, pass string) error {
	return repo.SetSSOConsentForm(ctx, s.database, state, hash, pass)
}
func (s *Service) HasSSOConsent(ctx context.Context, userID, siteID string) (bool, error) {
	return repo.HasSSOConsent(ctx, s.database, userID, siteID)
}
func (s *Service) GetSSOSignout(ctx context.Context, token string) (model.SSOSignout, error) {
	return repo.GetSSOSignout(ctx, s.database, token)
}
func (s *Service) SetSSOSignoutForm(ctx context.Context, token, formHash, cookieHash string) error {
	return repo.SetSSOSignoutForm(ctx, s.database, token, formHash, cookieHash)
}
func (s *Service) ForgetSSOConsent(ctx context.Context, userID, siteID string) error {
	return repo.ForgetSSOConsent(ctx, s.database, userID, siteID)
}
func (s *Service) ReplaceSSOPass(ctx context.Context, hash, keyHash, oldHash string, ttl, idle, keyIdle time.Duration) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = repo.ReplaceSSOPass(ctx, tx, hash, keyHash, oldHash, ttl, idle, keyIdle)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) RevokeVisitorSSO(ctx context.Context, sess model.VisitorSession, token, returnTo string) error {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = repo.RevokeVisitorSSO(ctx, tx, sess, token, returnTo)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) CompleteSSOSignout(ctx context.Context, token, cookieHash, formHash string) (string, error) {
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	back, err := repo.CompleteSSOSignout(ctx, tx, token, cookieHash, formHash)
	if err != nil {
		return "", err
	}
	return back, tx.Commit()
}

type SSOHandoff struct {
	Hop     model.SSOHop
	Once    string
	Status  int
	Failure string
}

func (s *Service) RedeemSSOHop(ctx context.Context, state string, pass model.SSOPass, post bool, formToken string, keyIdle, sessionIdle time.Duration) (SSOHandoff, error) {
	fail := func(status int, reason string, err error) (SSOHandoff, error) {
		return SSOHandoff{Status: status, Failure: reason}, err
	}
	tx, err := s.database.BeginTx(ctx, nil)
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
		if hop.ConsentPassHash.String != pass.Hash || !nonceMatches(formToken, hop.ConsentHash.String) {
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
	once, err := secureHex(32)
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
	return SSOHandoff{Hop: hop, Once: once}, nil
}
