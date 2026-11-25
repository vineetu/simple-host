// Package locks is who may open a site: one passcode for every address
// (pages and data), or only people the owner names by email, who sign in and
// may press "Ask for access". The routes are the two gate forms; the owner's
// changes are plain functions the site and AI-tool routes call.
package locks

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"simplehost/app"
	"simplehost/core/db"
	"simplehost/core/limits"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

// wrongTryDelay slows every wrong passcode; limits.PasscodeTries then locks
// the address out for a while.
const wrongTryDelay = 500 * time.Millisecond

// Mount adds the passcode form (route 34) and Ask for access (route 39) on
// every site address.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Site.Handle("POST /_sh/passcode", web.Func(h.unlock))
	a.Routes.Site.Handle("POST /_sh/ask-access", web.Func(h.askAccess))
}

type handler struct{ a *app.App }

// unlock checks the passcode form. Right: the unlock is remembered in this
// browser and the visitor goes back to the page. Wrong: the form again,
// after a pause; too many tries from one address lock it out for a while.
func (h *handler) unlock(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	if site.Access != "passcode" {
		http.Redirect(w, r, sites.Next(r), http.StatusSeeOther)
		return nil
	}
	if err := limits.Allow(r, limits.PasscodeTries, site.ID+"|"+limits.Address(r)); err != nil {
		ref := err.(*web.Refusal)
		w.Header().Set("Retry-After", strconv.Itoa(ref.RetryAfter))
		sites.GatePage(w, r, sites.NeedCode, http.StatusTooManyRequests, ref.Fix)
		return nil
	}
	code, err := h.a.Sites.OpenPasscode(site)
	if err != nil {
		return err
	}
	try := sha256.Sum256([]byte(strings.TrimSpace(r.PostFormValue("passcode"))))
	real := sha256.Sum256([]byte(code))
	if subtle.ConstantTimeCompare(try[:], real[:]) != 1 {
		time.Sleep(wrongTryDelay)
		sites.GatePage(w, r, sites.NeedCode, http.StatusUnauthorized, "That passcode is not right. Check it and try again.")
		return nil
	}
	h.a.Sites.SetUnlocked(w, r, site)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, sites.Next(r), http.StatusSeeOther)
	return nil
}

// askAccess tells the owner that the signed-in visitor wants to be named.
func (h *handler) askAccess(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	d, err := h.a.Sites.Visit(r, site)
	if err != nil {
		return err
	}
	switch d {
	case sites.Open:
		http.Redirect(w, r, sites.Next(r), http.StatusSeeOther)
		return nil
	case sites.AskAccess:
	default:
		sites.GatePage(w, r, d, 0, "")
		return nil
	}
	if err := limits.Allow(r, limits.AccessAsks, site.ID+"|"+web.CallerOf(r).ID); err != nil {
		sites.GatePage(w, r, d, http.StatusTooManyRequests, err.(*web.Refusal).Fix)
		return nil
	}
	if err := db.Event(r.Context(), h.a.DB, "access", site.OwnerID, site.ID, map[string]string{"email": web.CallerOf(r).Email, "site": site.Name}); err != nil {
		return err
	}
	sites.GatePage(w, r, d, http.StatusOK, "We told the owner. You can open the site once they add you.")
	return nil
}

// SetPasscode locks every address of the site, pages and data, with code.
// Unlocks of an earlier passcode end.
func SetPasscode(ctx context.Context, a *app.App, site *sites.Site, code string) error {
	code = strings.TrimSpace(code)
	n := utf8.RuneCountInString(code)
	bad := !utf8.ValidString(code) || n < 6 || n > 128 || strings.IndexFunc(code, unicode.IsControl) >= 0
	if bad {
		return web.Refuse(http.StatusBadRequest, "bad_passcode", "Use a passcode of 6 to 128 characters on one line.")
	}
	enc, err := a.Sites.SealPasscode(site.ID, code)
	if err != nil {
		return err
	}
	_, err = a.DB.ExecContext(ctx, `UPDATE sites SET access = 'passcode', passcode_enc = $2, passcode_gen = passcode_gen + 1 WHERE id = $1`, site.ID, enc)
	a.Sites.Forget(site.OwnerID, site.ID)
	return err
}

// Passcode is the site's passcode, for its owner to see again ("" when none).
func Passcode(a *app.App, site *sites.Site) (string, error) {
	if site.PasscodeEnc == nil {
		return "", nil
	}
	return a.Sites.OpenPasscode(site)
}

// SignEveryoneOut ends every remembered unlock and every visitor sign-in on
// the site. The passcode and the named people stay.
func SignEveryoneOut(ctx context.Context, a *app.App, site *sites.Site) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET passcode_gen = passcode_gen + 1 WHERE id = $1`, site.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE site_id = $1`, site.ID); err != nil {
		return err
	}
	err = tx.Commit()
	a.Sites.Forget("") // a locked site: nothing was kept for it
	return err
}

// Open lets anyone open the site again. The passcode and the list are kept
// for a later lock, but no longer apply.
func Open(ctx context.Context, a *app.App, site *sites.Site) error {
	_, err := a.DB.ExecContext(ctx, `UPDATE sites SET access = 'open' WHERE id = $1`, site.ID)
	a.Sites.Forget(site.OwnerID, site.ID)
	return err
}

// SetViewers opens the site only to these emails (and the owner and their
// team). Anyone left off loses access at once.
func SetViewers(ctx context.Context, a *app.App, site *sites.Site, emails []string) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ids []string
	for _, e := range emails {
		p, err := a.People.Ensure(ctx, e)
		if err != nil {
			return err
		}
		ids = append(ids, p.ID)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM viewers WHERE site_id = $1 AND NOT (person_id::text = ANY (string_to_array($2, ',')))`, site.ID, strings.Join(ids, ",")); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO viewers (site_id, person_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, site.ID, id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET access = 'viewers' WHERE id = $1`, site.ID); err != nil {
		return err
	}
	err = tx.Commit()
	a.Sites.Forget(site.OwnerID, site.ID)
	return err
}

// Viewer is one named person and whether they have signed in on the site yet.
type Viewer struct {
	Email    string `json:"email"`
	SignedIn bool   `json:"signed_in"`
}

// Viewers is the site's named people, by email.
func Viewers(ctx context.Context, a *app.App, site *sites.Site) ([]Viewer, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT p.email, EXISTS (SELECT 1 FROM visitor_signins s WHERE s.site_id = v.site_id AND s.person_id = v.person_id)
		FROM viewers v JOIN people p ON p.id = v.person_id WHERE v.site_id = $1 ORDER BY p.email`, site.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Viewer{}
	for rows.Next() {
		var v Viewer
		if err := rows.Scan(&v.Email, &v.SignedIn); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
