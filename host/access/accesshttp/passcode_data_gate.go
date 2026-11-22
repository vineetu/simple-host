package accesshttp

import (
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// --- The saved-data API ---

// passcodeGate wraps a page-facing route under /v1/sites/{sitename}/ or
// /v1/u/{handle}/sites/{sitename}/. On a protected site it lets through the
// owner's or the admin's key (or connector token), a valid unlock cookie
// for this host, and a read from a preview page whose token holds; anything
// else is 403 site_locked. Preflights, unknown sites and unprotected sites
// pass untouched (the route's own checks still run). The unlock cookie is
// never an owner power: it only gets a visitor as far as the route lets any
// visitor go.
func (h *AccessHandler) PasscodeGate(next http.Handler) http.Handler {
	return h.siteDataGate(next, true)
}

// signInGate is passcodeGate for the visitor sign-in routes (me, visitor/auth,
// verify): the passcode still applies, named viewers do not, since signing in
// is how a named viewer gets in (viewers.go).
func (h *AccessHandler) signInGate(next http.Handler) http.Handler {
	return h.siteDataGate(next, false)
}

func (h *AccessHandler) siteDataGate(next http.Handler, viewers bool) http.Handler {
	kind := accounts.VisitData
	if !viewers {
		kind = accounts.VisitSignIn
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		// The routes resolve a bare name two ways (resolveSiteID, and
		// resolveWriteSiteID, which prefers the caller's own same-named
		// site): both sites must let the request in.
		siteName := strings.TrimSpace(r.PathValue("sitename"))
		ids := map[string]bool{}
		if id, _, err := h.resolveSiteIDBare(r, siteName); err == nil {
			ids[id] = true
		}
		if id, err := h.resolveWriteSiteID(r, siteName); err == nil {
			ids[id] = true
		}
		allowed, private, gated := true, false, false
		for id := range ids {
			v := h.decideData(r, id, kind)
			switch v.verdict {
			case accounts.VisitServe:
			case accounts.VisitServiceError:
				httpx.WriteInternal(w)
				return
			case accounts.VisitNeedSignIn, accounts.VisitNotListed:
				allowed, private = false, true
			default:
				allowed = false
			}
			gated = gated || v.gated
		}
		if allowed {
			if gated {
				// A protected or named-viewers site's data is never stored by
				// any cache.
				w.Header().Set("Cache-Control", "private, no-store")
			}
			next.ServeHTTP(w, r)
			return
		}
		if private {
			writeSitePrivate(w)
			return
		}
		writeSiteLocked(w)
	})
}

func writeSiteLocked(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{
		Error: "this site asks for a passcode; a visitor enters it on the site's page first, and the owner's key or connector works without it",
		Code:  "site_locked",
	})
}

// PasscodeLetsIn reports whether a request may act on site siteID as a
// visitor by the passcode rule (accounts.PasscodeAdmits for the API): the
// site has no passcode, or the request passes it. Errors count as no.
func (h *AccessHandler) PasscodeLetsIn(r *http.Request, siteID string) bool {
	return h.decideData(r, siteID, accounts.VisitSignIn).verdict == accounts.VisitServe
}

// dataVisit is the visitor gate's answer for one site's data request, with
// the protection row it was decided on.
type dataVisit struct {
	verdict accounts.VisitVerdict
	gated   bool // the site has a passcode or named viewers
	row     model.SitePasscodeRow
	who     accounts.Visitor
}

// decideData loads site siteID's protection row and asks accounts.DecideVisit
// for an API request of the given kind.
func (h *AccessHandler) decideData(r *http.Request, siteID string, kind accounts.VisitKind) dataVisit {
	v := accounts.Visit{Kind: kind, Protected: true}
	v.Row, v.RowErr = h.sitesSvc().GetSitePasscode(r.Context(), siteID)
	gated := v.Gated()
	if gated {
		v.Who, _, _ = h.visitorFacts(r, v.Row, kind, true)
	}
	verdict := accounts.DecideVisit(v)
	if verdict == accounts.VisitServe && v.Who.AdminKey && !v.Who.OwnerKey {
		log.Printf("site_gate_admin_bypass %s %s site_id=%s", r.Method, r.Pattern, siteID)
	}
	return dataVisit{verdict: verdict, gated: gated, row: v.Row, who: v.Who}
}

// KeyActsFor: the request carries the key (or connector token) of someone who
// acts as the site's owner (accounts.ActsAsOwner), or of the admin.
func (h *AccessHandler) KeyActsFor(r *http.Request, row model.SitePasscodeRow) (owner, admin bool) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		return false, false
	}
	u, ok, err := h.resolveWriterKey(r.Context(), key)
	if err != nil || !ok {
		return false, false
	}
	owner, err = accounts.ActsAsOwner(r.Context(), h.Database, u.ID, row.UserID, row.Name)
	return err == nil && owner, u.IsAdmin
}

// previewRefererFor: the request comes from a preview page of this site on
// this host whose token still holds (a preview reads the site's data the
// way the live site would).
func (h *AccessHandler) previewRefererFor(r *http.Request, siteID string) bool {
	if !web.PreviewReferer(r) {
		return false
	}
	u, err := url.Parse(r.Header.Get("Referer"))
	if err != nil {
		return false
	}
	p := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(p, web.PreviewPathSegment+"/") {
		_, p, _ = strings.Cut(p, "/")
	}
	p = strings.TrimPrefix(p, web.PreviewPathSegment+"/")
	nStr, after, _ := strings.Cut(p, "/")
	token, _, _ := strings.Cut(after, "/")
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return false
	}
	id, v, err := web.OpenPreviewToken(h.ExportKey, h.Limits.PreviewLinkTTL, token, time.Now())
	return err == nil && id == siteID && v == n
}
