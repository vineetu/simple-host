package access

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// StartSSOHop starts the company sign-in's hop through the apex for site
// siteID on host, returning to returnTo: a fresh state (the hop's name on
// the apex) and nonce (the browser's proof, kept in its cookie; only its
// hash is stored).
func (s *Service) StartSSOHop(ctx context.Context, siteID, host, returnTo string) (state, nonce string, err error) {
	if nonce, err = secureHex(32); err != nil {
		return "", "", err
	}
	if state, err = secureHex(32); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(nonce))
	hop := model.SSOHop{State: state, SiteID: siteID, Host: host, ReturnTo: returnTo, NonceHash: base64.RawURLEncoding.EncodeToString(sum[:])}
	if err := repo.InsertSSOHop(ctx, s.database, hop); err != nil {
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
func (s *Service) BootstrapSSOPass(ctx context.Context, keyHash, cookieHash string, ttl, idle, keyIdle time.Duration) (raw string, err error) {
	if p, err := s.GetSSOPass(ctx, cookieHash, keyIdle); err == nil && p.ParentKeyHash == keyHash {
		return "", s.TouchSSOPass(ctx, p.Hash, idle)
	}
	if raw, err = secureHex(32); err != nil {
		return "", ErrSSOPassNotIssued
	}
	if err := s.ReplaceSSOPass(ctx, HashSecret(raw), keyHash, cookieHash, ttl, idle, keyIdle); err != nil {
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
func (s *Service) ConfirmSSOSignout(ctx context.Context, token, cookieHash string) (confirmed bool, form, returnTo string, err error) {
	ticket, err := s.GetSSOSignout(ctx, token)
	if err != nil {
		return false, "", "", ErrSSOSignoutUnknown
	}
	if ticket.PassHash != "" && ticket.PassHash == cookieHash {
		return true, "", "", nil
	}
	if form, err = secureHex(32); err != nil {
		return false, "", "", err
	}
	if err := s.SetSSOSignoutForm(ctx, token, HashSecret(form), cookieHash); err != nil {
		return false, "", "", err
	}
	return false, form, ticket.ReturnTo, nil
}
