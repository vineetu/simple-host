package access

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// The visitor sign-in's last step, GET /v1/visitor/establish on the site's
// own host: the one-time code is claimed for the pending session it stands
// for, then settled (activated, or deleted when anything refused it).

// ClaimEstablish spends the one-time code once on host and returns the token
// and the pending session it stands for, once this browser shows nonce (the
// one the sign-in started with) and the session is still live and was made
// on host for the site tok.ReturnTo is on. The code is burned before any
// check, so a code tried in the wrong browser or on the wrong host cannot be
// retried. onSiteHost: host is a site's own address (not the apex or the
// content host). A refusal is its status and reason; whenever the code was
// spent, tok is set and SettleEstablish must follow.
func (s *Service) ClaimEstablish(ctx context.Context, once, host, nonce string, onSiteHost bool, keyIdle time.Duration) (tok model.EstablishToken, sess model.VisitorSession, status int, reason string) {
	tok, err := repo.ConsumeEstablishToken(ctx, s.database, once, host)
	if err != nil {
		return model.EstablishToken{}, sess, http.StatusBadRequest, "code_used_or_expired"
	}
	if !onSiteHost {
		return tok, sess, http.StatusBadRequest, "wrong_host"
	}
	if !tok.NonceHash.Valid || nonce == "" {
		return tok, sess, http.StatusBadRequest, "nonce_missing"
	}
	if !nonceMatches(nonce, tok.NonceHash.String) {
		return tok, sess, http.StatusBadRequest, "nonce_mismatch"
	}
	// A revoked or expired parent, an offline site, or a passcode set while
	// the browser was on the apex must not yield a usable session.
	sess, err = s.VisitorSession(ctx, tok.SessionID, host, "", keyIdle)
	if err != nil {
		return tok, sess, http.StatusBadRequest, "session_invalid"
	}
	if target, err := url.Parse(tok.ReturnTo); err != nil || !strings.EqualFold(target.Hostname(), host) {
		return tok, sess, http.StatusBadRequest, "return_host"
	}
	return tok, sess, 0, ""
}
