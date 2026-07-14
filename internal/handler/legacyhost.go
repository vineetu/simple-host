package handler

import (
	"database/sql"
	"net/http"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// LegacyHostRedirect 301s the deprecated per-name subdomains — <name>.<siteDomain>
// (e.g. ideaflow.simple-host.app) — to their canonical v3 path URL on the content
// host (https://sites.<siteDomain>/<handle>/<name>/…), preserving path and query so
// existing links keep working. The name-subdomain model is retired; the path model
// is the single home for a site.
//
// Everything else passes straight through untouched: the content host itself, the
// apex and www, custom domains (a different host entirely), and multi-label hosts
// like x.lab.<siteDomain>. Only a single-label <name>.<siteDomain> is redirected.
func LegacyHostRedirect(siteDomain, contentHost string, database *sql.DB, next http.Handler) http.Handler {
	siteDomain = strings.ToLower(strings.TrimSpace(siteDomain))
	contentHost = strings.ToLower(strings.TrimSpace(contentHost))
	suffix := "." + siteDomain

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}

		// Not a name-subdomain of ours → leave it alone.
		if host == contentHost || host == siteDomain || !strings.HasSuffix(host, suffix) {
			next.ServeHTTP(w, r)
			return
		}
		label := strings.TrimSuffix(host, suffix)
		if label == "" || label == "www" || strings.Contains(label, ".") {
			next.ServeHTTP(w, r) // www, or a multi-label host (e.g. *.lab)
			return
		}

		// A deprecated <name>.<siteDomain>. Resolve the oldest owner's handle (the
		// account this host historically served) and 301 to the path URL. Unknown
		// name → send them to the home page rather than a dead end.
		handle, err := db.GetHandleBySiteName(r.Context(), database, label)
		if err != nil || handle == "" {
			http.Redirect(w, r, "https://"+siteDomain+"/", http.StatusMovedPermanently)
			return
		}
		target := "https://" + contentHost + "/" + handle + "/" + label + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}
