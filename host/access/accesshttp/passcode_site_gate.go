package accesshttp

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// --- The gate on the file-serving path ---

// SiteGate is the visitor gate every Go path to a site's files goes through:
// the site handler puts a sites.Serving on the request, the gate answers the
// take-down, offline, passcode or named-viewers page when one applies, and
// otherwise hands the request to Serving.Then. A protected site that lets the
// request in gets its private-cache headers here.
func (h *AccessHandler) SiteGate() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := appsites.ServingOf(r)
		if !ok || s.UserID == "" {
			h.servePasscodeGate(w, r, "", "", 0) // a protected request on a host the app does not know
			return
		}
		if !s.FrameOnly && h.GateRefuses(w, r, s) {
			return
		}
		h.ownerFrameGuard(w, r, s.UserID)
		s.Then.ServeHTTP(w, r)
	})
}

// GateRefuses reads the opened folder's markers, measures the visitor, asks
// accounts.DecideVisit and renders every verdict but serve as a page.
func (h *AccessHandler) GateRefuses(w http.ResponseWriter, r *http.Request, s appsites.Serving) bool {
	v, sess, email := h.pageVisit(r, s)
	verdict := accounts.DecideVisit(v)
	if v.Gated() {
		protectedHeaders(w)
	}
	if v.PastViewers(verdict) {
		h.viewerLetIn(w, r, sess)
	}
	if verdict == accounts.VisitServe {
		return false
	}
	h.servePageRefusal(w, r, s, v, verdict, sess, email)
	return true
}

// pageVisit is what the gate knows about a page request: the opened
// folder's markers and, for a protected site, its row (bound to the folder)
// and what the visitor carries.
func (h *AccessHandler) pageVisit(r *http.Request, s appsites.Serving) (v accounts.Visit, sess model.VisitorSession, email string) {
	present := func(name string) bool { _, err := s.Root.Lstat(name); return err == nil }
	v = accounts.Visit{Kind: accounts.VisitPage, Suspended: present("suspended"), Offline: present("offline"), Protected: present("passcode"), Bound: true,
		SharedHost: h.Hosts.IsSharedHost(httpx.HostName(r))}
	if v.Protected && !v.Suspended && !v.Offline {
		v.Row, v.RowErr = h.siteProtectionRow(r, s.Root, s.UserID, s.Name)
		if v.RowErr == nil && !v.SharedHost {
			v.Who, sess, email = h.visitorFacts(r, v.Row, accounts.VisitPage, false)
		}
	}
	return v, sess, email
}

// viewerLetIn: a page past the named-viewers rule keeps the visitor's
// session alive (a page visit only) and may be framed by the site's own
// pages only; browsers without fetch metadata still honour that header.
func (h *AccessHandler) viewerLetIn(w http.ResponseWriter, r *http.Request, sess model.VisitorSession) {
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
		_ = h.StoreAccounts().TouchVisitorSession(r.Context(), sess.ID, h.Limits.VisitorSessionIdle)
	}
	if !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		w.Header().Add("Content-Security-Policy", "frame-ancestors 'self'")
	}
}

// servePageRefusal renders a verdict other than serve as the page it shows.
func (h *AccessHandler) servePageRefusal(w http.ResponseWriter, r *http.Request, s appsites.Serving, v accounts.Visit, verdict accounts.VisitVerdict, sess model.VisitorSession, email string) {
	switch verdict {
	case accounts.VisitTakedown:
		web.ServeTakedownPage(w, r, h.pipeline())
	case accounts.VisitOffline:
		web.ServeOfflinePage(w, r, h.pipeline())
	case accounts.VisitNotFound:
		http.NotFound(w, r)
	case accounts.VisitServiceError:
		if v.RowErr != nil {
			log.Printf("passcode: site %s/%s: %v", s.UserID, s.Name, v.RowErr)
		} else {
			log.Printf("viewers: site %s: %v", v.Row.SiteID, v.Who.ListErr)
		}
		web.RenderServiceErrorPage(w, r, h.pipeline())
	case accounts.VisitElsewhere:
		h.passcodeElsewhere(w, r, v.Row, s.Rel)
	case accounts.VisitFramedOut:
		writeOAuthHTMLError(h.pipeline(), w, http.StatusForbidden)
	case accounts.VisitNeedSignIn:
		if !h.startGateSSO(w, r) {
			h.serveViewerPage(w, r, viewerPageSignIn, gateNext(r), "", "")
		}
	case accounts.VisitNotListed:
		// The owner hears about it once a day per person, for a page visit
		// only (a stray subresource is not someone asking to be let in).
		if dest := r.Header.Get("Sec-Fetch-Dest"); dest == "" || dest == "document" {
			h.emitSiteEvent(appsites.SiteEvent{SiteID: v.Row.SiteID, Name: "viewer.access_requested", Data: map[string]any{"visitor": sess.UserID}, Dedupe: 24 * time.Hour})
		}
		h.serveViewerPage(w, r, viewerPagePrivate, gateNext(r), email, "", h.ssoBaseURL)
	case accounts.VisitNeedPasscode:
		h.servePasscodeGate(w, r, gateNext(r), "", 0)
	}
}

// visitorFacts measures what the request carries toward a protected site,
// for accounts.DecideVisit: only the facts the row and the request make
// relevant are looked up; keys says whether the request's key counts (never
// for a page, nor for a caller storage classed as a visitor). It also returns the visitor's session and, for a signed-in person, the
// address they present (the private page names it).
func (h *AccessHandler) visitorFacts(r *http.Request, row model.SitePasscodeRow, kind accounts.VisitKind, keys bool) (who accounts.Visitor, sess model.VisitorSession, email string) {
	pass := r.Header.Get(VisitorPassHeader) != ""
	who = accounts.Visitor{
		Read:       r.Method == http.MethodGet || r.Method == http.MethodHead,
		CrossSite:  crossSiteSubresource(r),
		Framed:     framedElsewhere(r),
		SameOrigin: pass || httpx.SameOrigin(r),
	}
	if kind != accounts.VisitPage {
		if keys {
			who.OwnerKey, who.AdminKey = h.KeyActsFor(r, row)
		}
		who.Preview = who.Read && h.previewRefererFor(r, row.SiteID)
	}
	if row.Enc != nil {
		who.Unlocked = h.unlocked(r, row.SiteID, row.Generation)
	}
	// The session and the list are looked up only when the named-viewers rule
	// will read them, so a request costs the queries it did before the gate
	// was one decision.
	needSession := !who.CrossSite && !who.Framed
	if kind != accounts.VisitPage {
		needSession = !who.OwnerKey && !who.AdminKey && !(who.Read && who.Preview) && (who.SameOrigin || who.Read && needSession)
	}
	if row.NamedViewers && kind != accounts.VisitSignIn && needSession {
		sess, who.Session = h.viewerSession(r, row.SiteID)
		if who.Session {
			if kind != accounts.VisitPage && who.SameOrigin && !pass {
				papermetrics.From(r.Context()).Member(r.Context(), sess.UserID)
			}
			who.Listed, email, who.ListErr = h.viewerAllowed(r, row, sess.UserID)
		}
	}
	return who, sess, email
}

// ownerFrameGuard: a page the site's owner opens while signed in on this
// host is never shown inside another origin's frame. There the page acts
// with owner rights for saved data (storageOwnerOnSite), and every
// <site>.<handle>.simple-host.app host is one "site" to a browser, so the
// Lax session cookie goes along when a sibling site frames it (clickjacking).
// Other visitors' pages are unchanged.
//
// On Simple Hack the owner is the team's members or the event's organisers
// (storageOwnerOnSite), and sibling team sites share one registrable domain
// the same way.
func (h *AccessHandler) ownerFrameGuard(w http.ResponseWriter, r *http.Request, ownerID string) {
	// A copy cached without the cookie must not stand in for one with it.
	w.Header().Add("Vary", "Cookie")
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "empty", "image", "script", "style", "font", "audio", "video", "track", "manifest", "worker", "sharedworker", "serviceworker":
		return // never framed (empty: fetch and XHR); no database lookup per subresource
	}
	sess, err := visitorCookieSession(r, h.Database, "", h.Limits.KeyIdleExpiry)
	if err != nil {
		return
	}
	if h.storageBindings != nil {
		papermetrics.From(r.Context()).Member(r.Context(), sess.UserID)
		if ok, err := h.storageBindingProvider().OwnerOnSite(r.Context(), sess.SiteID, sess.UserID); err != nil || !ok {
			return
		}
	} else if sess.UserID != ownerID {
		// A member of the team that holds the site, or an editor of it, is
		// an owner here too (accounts.ActsAsOwner, through the default
		// provider); a failed lookup keeps the frame guard.
		if ok, err := h.storageBindingProvider().OwnerOnSite(r.Context(), sess.SiteID, sess.UserID); err == nil && !ok {
			return
		}
	}
	setOwnerFrameHeaders(w)
}

// protectedHeaders: nothing of a protected (or named-viewers) site is ever
// stored by a shared cache, served to another site's page, or indexed.
func protectedHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Add("Vary", "Cookie")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

// siteProtectionRow binds the by-name lookup to the directory being served.
// Rename and deletion hold this same lock while moving the directory and row;
// neither may change the binding between the identity check and the query.
func (h *AccessHandler) siteProtectionRow(r *http.Request, root *os.Root, userID, siteName string) (model.SitePasscodeRow, error) {
	if root != nil {
		// Exports and publication share this mutex. Bound the visitor's wait
		// without leaving a goroutine behind to acquire it after cancellation.
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		mu, _ := h.UploadLocks.LoadOrStore(userID+"/"+siteName, &sync.Mutex{})
		m := mu.(*sync.Mutex)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if err := ctx.Err(); err != nil {
				return model.SitePasscodeRow{}, err
			}
			if m.TryLock() {
				break
			}
			select {
			case <-ctx.Done():
				return model.SitePasscodeRow{}, ctx.Err()
			case <-tick.C:
			}
		}
		defer m.Unlock()
		opened, err := root.Stat(".")
		if err != nil {
			return model.SitePasscodeRow{}, err
		}
		current, err := os.Stat(h.Disk.SiteDir(userID, siteName))
		if os.IsNotExist(err) {
			return model.SitePasscodeRow{}, sql.ErrNoRows
		}
		if err != nil {
			return model.SitePasscodeRow{}, err
		}
		if !os.SameFile(opened, current) {
			return model.SitePasscodeRow{}, sql.ErrNoRows
		}
	}
	return h.sitesSvc().GetSitePasscodeByName(r.Context(), userID, siteName)
}

// passcodeElsewhere answers a protected site's file on a shared host: a 302
// to the same path on the site's own address, or, when it has none, the
// plain protected page without a form.
func (h *AccessHandler) passcodeElsewhere(w http.ResponseWriter, r *http.Request, row model.SitePasscodeRow, rel string) {
	escRel := "/" + strings.TrimLeft((&url.URL{Path: rel}).EscapedPath(), "/")
	query := ""
	if r.URL.RawQuery != "" {
		query = "?" + r.URL.RawQuery
	}
	target := ""
	if info, has, err := h.Hosts.SiteOwnAddress(r.Context(), row.SiteID); err == nil && has {
		target = "https://" + strings.ToLower(info.Domain) + escRel
	} else if handle, _, name, err := h.sitesSvc().GetSiteOwner(r.Context(), row.SiteID); err == nil && handle != "" && h.Hosts.Addressing().PersonAddressFor(handle, name) {
		target = h.Hosts.Addressing().SiteAddressWithPath(handle, name, escRel)
	}
	if target != "" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, target+query, http.StatusFound)
		return
	}
	if row.NamedViewers {
		h.serveViewerPage(w, r, viewerPageSignIn, "", "", "")
		return
	}
	h.servePasscodeGate(w, r, "", "", 0)
}

// gateNext is where the gate sends the visitor back to after the passcode:
// the page they asked for, on this host (never nginx's or Caddy's internal
// rewrite of it: safeNext strips that).
func gateNext(r *http.Request) string {
	p := r.URL.EscapedPath()
	if r.URL.RawQuery != "" {
		p += "?" + r.URL.RawQuery
	}
	return safeNext(p)
}

// safeNext keeps a path on this host: it must start with one "/" (never
// "//" or "/\", which a browser reads as another host), hold no control
// characters and never be one of the app's /internal/ pages (reachable only
// through nginx's own rewrites, so a 404 to a visitor): the passcode
// rewrite gives back the path the visitor asked for, any other /internal/
// path is "/". Anything else is "/".
func safeNext(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") || len(p) > 2048 {
		return "/"
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f || c == '\\' {
			return "/"
		}
	}
	p = web.StripPasscodeRewrite(p)
	if strings.HasPrefix(p, "//") {
		return "/"
	}
	path, _, _ := strings.Cut(p, "?")
	if dec, err := url.PathUnescape(path); err == nil {
		path = dec
	}
	if path == "/internal" || strings.HasPrefix(path, "/internal/") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, "/\\") {
		return "/"
	}
	return p
}

func setOwnerFrameHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	if !strings.Contains(strings.Join(w.Header().Values("Content-Security-Policy"), ";"), "frame-ancestors 'self'") {
		w.Header().Add("Content-Security-Policy", "frame-ancestors 'self'")
	}
}
