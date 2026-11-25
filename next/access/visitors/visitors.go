// Package visitors is a visitor signing in on one site (F18). The site
// sends the browser to the platform with a sealed hop; the platform proves
// the email (access/signin) and sends back a sealed ticket; the site turns
// the ticket into its own session cookie. The sign-in belongs to that one
// site: no other site can read it.
package visitors

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

const hopLife = 10 * time.Minute

var errTicket = web.Refuse(http.StatusBadRequest, "sign_in_expired", "This sign-in was started in another browser or ran out. Sign in again.")

type handler struct{ a *app.App }

// Mount adds routes 35 to 38 on every site address.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Site.Handle("GET /_sh/signin", web.Func(h.start))
	a.Routes.Site.Handle("GET /_sh/signin/return", web.Func(h.finish))
	a.Routes.Site.Handle("POST /_sh/signout", web.Func(h.signOut))
	a.Routes.Site.Handle("GET /_sh/me", web.Func(h.me))
}

// start is route 35: remember a nonce on this host and go to the platform's
// sign-in page with a hop naming this site, this host and where to return.
func (h *handler) start(w http.ResponseWriter, r *http.Request) error {
	site := sites.PlaceOf(r).Site
	back := r.URL.Query().Get("return")
	if back == "" {
		back = "/"
	}
	if !web.LocalPath(back) {
		return web.Refuse(http.StatusBadRequest, "bad_return", "Give return as a path on this site, like /bookings.")
	}
	nonce := access.NewSecret("")
	access.SetCookie(w, r, "sh_vnonce", nonce, hopLife)
	hop := access.SealHop(access.Hop{Site: site.ID, Host: web.Host(r), Return: back, Nonce: access.Hash(nonce)}, hopLife)
	http.Redirect(w, r, "https://"+h.a.Config.AppDomain+"/signin?hop="+url.QueryEscape(hop), http.StatusFound)
	return nil
}

// finish is route 36: check the ticket belongs to this site, this host and
// this browser, then set this site's session cookie and go back. The person
// proved their email or confirmed this site on the platform, never here: a
// page on this origin is the owner's and could press a button here itself.
func (h *handler) finish(w http.ResponseWriter, r *http.Request) error {
	site := sites.PlaceOf(r).Site
	t, err := access.OpenHop(r.URL.Query().Get("ticket"))
	if err != nil || t.Person == "" || !web.LocalPath(t.Return) || t.Site != site.ID || t.Host != web.Host(r) || t.Nonce != access.Hash(access.Cookie(r, "sh_vnonce")) {
		return errTicket
	}
	if _, err := h.a.DB.ExecContext(r.Context(), `INSERT INTO visitor_signins (site_id, person_id, confirmed) VALUES ($1, $2, true)
		ON CONFLICT (site_id, person_id) DO UPDATE SET confirmed = true`, site.ID, t.Person); err != nil {
		return err
	}
	if err := h.a.People.StartSiteSession(w, r, t.Person, site.ID); err != nil {
		return err
	}
	access.SetCookie(w, r, "sh_vnonce", "", 0)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, t.Return, http.StatusFound)
	return nil
}

// signOut is route 37: end this site's session in this browser. A form
// post (a gate page's button) goes back to its page; a script gets JSON.
func (h *handler) signOut(w http.ResponseWriter, r *http.Request) error {
	id := access.SiteSessionID(access.Cookie(r, "sh_vsess"))
	if _, err := h.a.DB.ExecContext(r.Context(), `DELETE FROM visitor_sessions WHERE id = $1 AND site_id = $2`, id, sites.PlaceOf(r).Site.ID); err != nil {
		return err
	}
	access.SetCookie(w, r, "sh_vsess", "", 0)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		// The Sign out button of a gate page (a plain form): back to the page.
		http.Redirect(w, r, sites.Next(r), http.StatusSeeOther)
		return nil
	}
	web.JSON(w, http.StatusOK, map[string]bool{"signed_in": false})
	return nil
}

// me is route 38: who is signed in here.
func (h *handler) me(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	w.Header().Set("Cache-Control", "no-store")
	if c.ID == "" {
		web.JSON(w, http.StatusOK, map[string]any{"signed_in": false})
		return nil
	}
	web.JSON(w, http.StatusOK, map[string]any{"signed_in": true, "id": c.ID, "email": c.Email})
	return nil
}
