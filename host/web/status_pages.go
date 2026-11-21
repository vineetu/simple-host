package web

import "net/http"

// The hosted status pages a site's own addresses answer while it cannot be
// served: taken down by the operator, or offline by its owner.

// TakedownPage is what every address of a suspended site answers. Plain on
// purpose: it is served on the site's own origins (custom domains included),
// so it loads nothing and names no one.
var TakedownPage = Themed(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><!--sh:theme--><title>Site taken down</title>
<style>body{font:17px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a2233;background:#fff;margin:0;padding:15vh 20px;text-align:center}h1{font-size:26px;margin:0 0 8px}p{color:#5b6576;margin:0}html[data-theme=dark] body{color:#e6ebf3;background:#0b1222}html[data-theme=dark] p{color:#a3afc1}</style>
</head><body><h1>This site has been taken down</h1><p>It is no longer available.</p></body></html>
`)

// OfflinePage is served on the site's own origins (custom domains included),
// so it loads nothing and names no one.
var OfflinePage = Themed(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><!--sh:theme--><title>Site offline</title>
<style>body{font:17px/1.5 system-ui,-apple-system,Segoe UI,Roboto,sans-serif;color:#1a2233;background:#fff;margin:0;padding:15vh 20px;text-align:center}h1{font-size:26px;margin:0 0 8px}p{color:#5b6576;margin:0}html[data-theme=dark] body{color:#e6ebf3;background:#0b1222}html[data-theme=dark] p{color:#a3afc1}</style>
</head><body><h1>This site is offline</h1><p>Its owner has taken it offline for now.</p></body></html>
`)

// ServeTakedownPage writes the take-down page. 410 so it is not cached as the
// site's content; no-store so a restore takes effect on the next load.
func ServeTakedownPage(w http.ResponseWriter, r *http.Request, pages *AssetPipeline) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusGone)
	if r.Method != http.MethodHead {
		_, _ = w.Write(pages.HostedStatusPage(TakedownPage))
	}
}

// ServeOfflinePage writes the offline page: 503 (a pause, not a removal) and
// no-store, so switching the site back on takes effect on the next load.
func ServeOfflinePage(w http.ResponseWriter, r *http.Request, pages *AssetPipeline) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method != http.MethodHead {
		_, _ = w.Write(pages.HostedStatusPage(OfflinePage))
	}
}
