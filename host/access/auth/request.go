package auth

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
)

// RefuseForeign refuses (403) a browser request from a page that is not on
// origin; a request with neither header (an agent) passes.
func RefuseForeign(w http.ResponseWriter, r *http.Request, origin string) bool {
	if o := r.Header.Get("Origin"); o != "" && !strings.EqualFold(strings.TrimRight(o, "/"), origin) {
		httpx.WriteError(w, http.StatusForbidden, "origin_not_allowed", "cross-origin request refused")
		return true
	}
	if httpx.CrossSite(r) {
		httpx.WriteError(w, http.StatusForbidden, "origin_not_allowed", "cross-site request refused")
		return true
	}
	return false
}
