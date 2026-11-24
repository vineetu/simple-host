// Package publicpage is a person's or team's own page at <handle>.simple-host.app
// (existing accounts) or <handle>.simple-host.site (new): the sites they
// switched on for it, or one of their sites in its place (routes 52 and 53).
package publicpage

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"simplehost/app"
	"simplehost/core/web"
	sites "simplehost/sites/core"
)

type handler struct{ a *app.App }

// Mount adds routes 52 and 53.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Page.Handle("/", web.Func(h.page)) // every method: a home site's forms post here too
	a.Routes.Page.Handle("GET /sitemap.xml", web.Func(h.sitemap))
}

// page lists the owner's listed sites. With a home site chosen, that site
// answers here instead, every path of it, exactly as on its own address.
func (h *handler) page(w http.ResponseWriter, r *http.Request) error {
	o := sites.PlaceOf(r).Owner
	if o == nil || o.Suspended {
		return web.NotFound
	}
	var homeID sql.NullString
	err := h.a.DB.QueryRowContext(r.Context(), `SELECT home_site_id FROM owner_addresses WHERE owner_id = $1`, o.ID).Scan(&homeID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if homeID.Valid {
		if home, err := h.a.Sites.ByID(r.Context(), homeID.String); err == nil {
			c, err := h.a.People.Caller(r, home.ID)
			if err != nil {
				return err
			}
			h.a.Routes.Site.ServeHTTP(w, web.WithCaller(sites.WithPlace(r, sites.Place{Site: home}), c))
			return nil
		}
	}
	if r.URL.Path != "/" || r.Method != http.MethodGet && r.Method != http.MethodHead {
		return web.NotFound
	}
	list, err := h.sites(r, `s.listed AND NOT s.offline AND s.taken_down_at IS NULL AND s.live_version > 0`, o.ID)
	if err != nil {
		return err
	}
	name := o.DisplayName
	if name == "" {
		name = o.Handle
	}
	var items strings.Builder
	for _, s := range list {
		fmt.Fprintf(&items, `<li><a href="%s">%s</a></li>`, html.EscapeString(h.a.Sites.URL(s.Site)), html.EscapeString(s.Name))
	}
	if len(list) == 0 {
		items.WriteString("<li>Nothing here yet.</li>")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title>
<style>body{font:17px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:#1a2233;background:#fff;max-width:640px;margin:0 auto;padding:10vh 20px}
h1{font-size:28px;margin:0 0 16px}ul{list-style:none;padding:0}li{padding:10px 0;border-bottom:1px solid #e6e9ef}a{color:#1a4fd6;text-decoration:none}</style>
</head><body><h1>%s</h1><ul>%s</ul></body></html>`, html.EscapeString(name), html.EscapeString(name), items.String())
	return nil
}

// sitemap lists this page and every open site of the owner: not locked,
// not offline, not taken down.
func (h *handler) sitemap(w http.ResponseWriter, r *http.Request) error {
	o := sites.PlaceOf(r).Owner
	if o == nil || o.Suspended {
		return web.NotFound
	}
	list, err := h.sites(r, `s.access = 'open' AND NOT s.offline AND s.taken_down_at IS NULL AND s.live_version > 0`, o.ID)
	if err != nil {
		return err
	}
	type entry struct {
		Loc     string `xml:"loc"`
		LastMod string `xml:"lastmod,omitempty"`
	}
	out := struct {
		XMLName xml.Name `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
		URLs    []entry  `xml:"url"`
	}{URLs: []entry{{Loc: "https://" + web.Host(r) + "/"}}}
	for _, s := range list {
		out.URLs = append(out.URLs, entry{h.a.Sites.URL(s.Site), s.changed.UTC().Format(time.RFC3339)})
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write([]byte(xml.Header))
	return xml.NewEncoder(w).Encode(out)
}

type site struct {
	*sites.Site
	changed time.Time
}

// sites are the owner's live sites matching where, by name.
func (h *handler) sites(r *http.Request, where, ownerID string) ([]site, error) {
	rows, err := h.a.DB.QueryContext(r.Context(), `SELECT s.id, s.changed_at FROM sites s WHERE s.owner_id = $1 AND s.deleted_at IS NULL AND `+where+` ORDER BY s.name`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []site
	for rows.Next() {
		var x site
		var id string
		if err := rows.Scan(&id, &x.changed); err != nil {
			return nil, err
		}
		x.Site = &sites.Site{ID: id}
		ids = append(ids, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range ids {
		s, err := h.a.Sites.ByID(r.Context(), ids[i].ID)
		if err != nil {
			return nil, err
		}
		ids[i].Site = s
	}
	return ids, nil
}

// Home is the name of the site the owner chose for their public page ("" for the list).
func Home(r *http.Request, a *app.App, ownerID string) (string, error) {
	var name string
	err := a.DB.QueryRowContext(r.Context(), `SELECT s.name FROM owner_addresses o JOIN sites s ON s.id = o.home_site_id
		WHERE o.owner_id = $1 AND s.deleted_at IS NULL`, ownerID).Scan(&name)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return name, err
}

// SetHome makes one of the owner's sites their public page, or with "" the
// list of listed sites again.
func SetHome(r *http.Request, a *app.App, ownerID, name string) error {
	var siteID any
	if name != "" {
		s, err := a.Sites.Get(r.Context(), ownerID, name)
		if err != nil {
			return web.Refuse(http.StatusNotFound, "no_site", "You have no site called "+name+". Give one of your site names, or empty for the list.")
		}
		siteID = s.ID
	}
	_, err := a.DB.ExecContext(r.Context(), `INSERT INTO owner_addresses (owner_id, home_site_id) VALUES ($1, $2)
		ON CONFLICT (owner_id) DO UPDATE SET home_site_id = $2`, ownerID, siteID)
	return err
}
