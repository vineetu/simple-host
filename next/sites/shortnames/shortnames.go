// Package shortnames gives a site a free <name>.simple-host.app address in
// one step, live at once, first come first served. A site has at most one.
// A name let go is retired for good, as handles are, so an old link never
// reaches a stranger's site. The site changes (route 24) call these.
package shortnames

import (
	"database/sql"
	"net/http"

	access "simplehost/access/core"
	"simplehost/app"
	sites "simplehost/sites/core"
)

// Claim gives the site the short name. Its earlier short name, if any, is
// let go. A taken or retired name is refused, naming it.
func Claim(r *http.Request, a *app.App, site *sites.Site, name string) (string, error) {
	if err := a.Sites.Manage(r, site); err != nil {
		return "", err
	}
	old, err := Of(r, a, site)
	if err != nil || old == name {
		return "https://" + name + "." + a.Config.AppDomain + "/", err
	}
	tx, err := a.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if err := access.ClaimName(r.Context(), tx, name, "short", "", site.ID); err != nil {
		return "", err
	}
	if old != "" {
		if err := access.RetireName(r.Context(), tx, old); err != nil {
			return "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	a.Sites.Forget()
	return "https://" + name + "." + a.Config.AppDomain + "/", nil
}

// Release lets the site's short name go. It stops reaching the site at once.
func Release(r *http.Request, a *app.App, site *sites.Site) error {
	if err := a.Sites.Manage(r, site); err != nil {
		return err
	}
	old, err := Of(r, a, site)
	if err != nil || old == "" {
		return err
	}
	if err := access.RetireName(r.Context(), a.DB, old); err != nil {
		return err
	}
	a.Sites.Forget()
	return nil
}

// Of is the site's short name, or "".
func Of(r *http.Request, a *app.App, site *sites.Site) (string, error) {
	var name string
	err := a.DB.QueryRowContext(r.Context(), `SELECT name FROM names WHERE site_id = $1 AND kind = 'short' AND retired_at IS NULL`, site.ID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}
