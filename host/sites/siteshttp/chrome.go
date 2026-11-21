package siteshttp

import (
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
)

// chromeBase is the link base for the showcase and 404 pages, which are also
// served off the content host (nginx proxies those as /internal/…) and custom
// domains, where a relative link would land on the wrong server.
func (h *SiteHandler) chromeBase(r *http.Request) string {
	// Pages rendered for the content host (/internal/...) or on a person host
	// link back to the main site absolutely.
	if strings.HasPrefix(r.URL.Path, "/internal/") || h.Hosts.IsPlatformSubdomain(httpx.HostName(r)) || h.Hosts.IsSiteHost(r.Context(), httpx.HostName(r)) {
		return h.mainSiteURL()
	}
	return ""
}
