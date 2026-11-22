package siteshttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/web"
)

// Taking a site offline (the owner's switch).
//
// An offline site keeps everything (files, versions, saved data, lists,
// addresses). Every address answers a plain "This site is offline" page and
// visitor saves are refused; the owner's key and connector still deploy, read
// and write, so the site can be prepared and switched back on at will. The
// database is the record (sites.offline_at); an `offline` marker file in the
// site folder is what the servers that read files straight from disk test,
// after the operator's take-down marker, which wins.

// ServeOffline writes the offline page: 503 (a pause, not a removal) and
// no-store, so switching the site back on takes effect on the next load.
func (h *SiteHandler) ServeOffline(w http.ResponseWriter, r *http.Request) {
	web.ServeOfflinePage(w, r, h.Pages())
}
