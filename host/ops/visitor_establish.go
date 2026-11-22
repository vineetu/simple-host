package ops

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"time"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

// ClaimVisitorEstablish is access.ClaimEstablish plus the check that host's
// binding still names the immutable site the session was made for (hosts'
// return site for tok.ReturnTo's path, else a verified custom domain). A
// refusal is its status and reason; whenever the code was spent tok is set
// and access.SettleEstablish must follow.
func ClaimVisitorEstablish(ctx context.Context, database *sql.DB, hosts *sites.Hosts, once, host, nonce string, onSiteHost bool, keyIdle time.Duration) (model.EstablishToken, model.VisitorSession, int, string) {
	tok, sess, status, reason := access.New(database).ClaimEstablish(ctx, once, host, nonce, onSiteHost, keyIdle)
	if reason != "" {
		return tok, sess, status, reason
	}
	target, _ := url.Parse(tok.ReturnTo)
	boundID, ok := hosts.ReturnSite(ctx, host, target.Path)
	if !ok {
		info, err := sites.GetSiteByCustomDomain(ctx, database, host)
		if err != nil || !info.VerifiedAt.Valid {
			return tok, sess, http.StatusBadRequest, "site_unbound"
		}
		boundID = info.SiteID
	}
	if boundID != sess.SiteID {
		return tok, sess, http.StatusBadRequest, "site_moved"
	}
	return tok, sess, 0, ""
}
