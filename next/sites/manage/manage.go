// Package manage is a person's sites: list and search them (route 21), move
// one to the 7-day bin (route 25) and restore it, take it offline and back,
// and make an older kept version live. The site changes (route 24) call the
// exported functions; each checks the caller may manage the site.
package manage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

// BinDays is how long a deleted site can be restored.
const BinDays = 7

type handler struct{ a *app.App }

// Mount adds routes 21 and 25.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("GET /v1/sites", web.Func(h.list))
	a.Routes.Platform.Handle("DELETE /v1/sites/{site}", web.Func(h.remove))
}

// Listed is one site in the list.
type Listed struct {
	Name      string     `json:"name"`
	Owner     string     `json:"owner"`
	URL       string     `json:"url"`
	Version   int        `json:"version"`
	ChangedAt time.Time  `json:"changed_at"`
	Offline   bool       `json:"offline"`
	Listed    bool       `json:"listed"`
	Access    string     `json:"access"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// list is route 21: my sites and my teams' sites, most recently changed
// first. ?q= searches names; ?bin=true lists the bin instead.
func (h *handler) list(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as Authorization: Bearer <key>.")
	}
	rows, err := h.a.DB.QueryContext(r.Context(), `SELECT s.name, COALESCE(n.name, ''), COALESCE(a.app_shape, false), s.live_version, s.changed_at,
		s.offline, s.listed, s.access, s.deleted_at
		FROM sites s LEFT JOIN names n ON n.person_id = s.owner_id AND n.kind = 'handle' AND n.retired_at IS NULL
		LEFT JOIN owner_addresses a ON a.owner_id = s.owner_id
		WHERE (s.owner_id = $1 OR s.owner_id IN (SELECT team_id FROM team_members WHERE person_id = $1))
		AND (s.deleted_at IS NOT NULL) = $2 AND strpos(s.name, lower($3)) > 0
		ORDER BY COALESCE(s.deleted_at, s.changed_at) DESC LIMIT 1000`, c.ID, r.URL.Query().Get("bin") == "true", r.URL.Query().Get("q"))
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []Listed{}
	for rows.Next() {
		var x Listed
		var s sites.Site
		if err := rows.Scan(&x.Name, &x.Owner, &s.AppShape, &x.Version, &x.ChangedAt, &x.Offline, &x.Listed, &x.Access, &x.DeletedAt); err != nil {
			return err
		}
		s.Name, s.OwnerHandle = x.Name, x.Owner
		x.URL = h.a.Sites.URL(&s)
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"sites": out})
	return nil
}

// remove is route 25: the site goes to the bin with everything, for BinDays.
func (h *handler) remove(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err != nil {
		return err
	}
	if err := h.a.Sites.Manage(r, site); err != nil {
		return err
	}
	if _, err := h.a.DB.ExecContext(r.Context(), `UPDATE sites SET deleted_at = now() WHERE id = $1`, site.ID); err != nil {
		return err
	}
	h.a.Sites.Forget()
	web.JSON(w, http.StatusOK, map[string]any{"deleted": site.Name, "restore_until": time.Now().Add(BinDays * 24 * time.Hour)})
	return nil
}

// Restore brings the owner's site named name back from the bin with its
// files, versions, saved data and addresses.
func Restore(r *http.Request, a *app.App, ownerID, name string) (*sites.Site, error) {
	if ok, err := a.People.ActsFor(r.Context(), web.CallerOf(r), ownerID); err != nil || !ok {
		if err == nil {
			err = web.Refuse(http.StatusForbidden, "not_yours", "Only the site's owner or their team can do this.")
		}
		return nil, err
	}
	if _, err := a.Sites.Get(r.Context(), ownerID, name); err == nil {
		return nil, web.Refuse(http.StatusConflict, "name_taken", "A live site is already called "+name+". Delete it first, or publish under another name.")
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE sites SET deleted_at = NULL, changed_at = now() WHERE id = (SELECT id FROM sites
		WHERE owner_id = $1 AND name = $2 AND deleted_at IS NOT NULL ORDER BY deleted_at DESC LIMIT 1)`, ownerID, name)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, web.Refuse(http.StatusNotFound, "not_in_bin", "No site called "+name+" is in the bin. Sites stay there 7 days.")
	}
	a.Sites.Forget()
	return a.Sites.Get(r.Context(), ownerID, name)
}

// SetOffline takes a site offline (every address shows "offline", everything
// is kept) or back online.
func SetOffline(r *http.Request, a *app.App, site *sites.Site, offline bool) error {
	return change(r, a, site, `UPDATE sites SET offline = $2, changed_at = now() WHERE id = $1`, offline)
}

// SetListed puts the site on its owner's public page, or takes it off.
func SetListed(r *http.Request, a *app.App, site *sites.Site, listed bool) error {
	return change(r, a, site, `UPDATE sites SET listed = $2 WHERE id = $1`, listed)
}

// Rollback makes kept version n live at once on every address.
func Rollback(r *http.Request, a *app.App, site *sites.Site, n int) error {
	err := change(r, a, site, `UPDATE sites SET live_version = $2, changed_at = now() WHERE id = $1
		AND EXISTS (SELECT 1 FROM versions WHERE site_id = $1 AND n = $2)`, n)
	if errors.Is(err, errNoRow) {
		return web.Refuse(http.StatusNotFound, "no_version", fmt.Sprintf("Version %d is not kept. Pick one of the site's listed versions.", n))
	}
	return err
}

var errNoRow = errors.New("no row changed")

func change(r *http.Request, a *app.App, site *sites.Site, query string, arg any) error {
	if err := a.Sites.Manage(r, site); err != nil {
		return err
	}
	res, err := a.DB.ExecContext(r.Context(), query, site.ID, arg)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNoRow
	}
	a.Sites.Forget()
	return nil
}

// Work empties the bin: sites deleted more than BinDays ago go for good,
// with their folder (versions, saved data, stored files). Hourly.
func Work(ctx context.Context, a *app.App) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		rows, err := a.DB.QueryContext(ctx, `DELETE FROM sites WHERE deleted_at < now() - make_interval(days => $1) RETURNING id`, BinDays)
		for err == nil && rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				os.RemoveAll(filepath.Join(a.Config.DataDir, "sites", id))
			}
		}
		if err == nil {
			err = rows.Err()
			rows.Close()
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("emptying the bin: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
