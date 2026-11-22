package registration

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
)

// applyLimited meters POST apply: a signed-in caller is metered by both its
// account and its network address (RATE_LIMIT_EVENT_APPLY_USER/_IP), so one
// stranger cannot burn through many accounts from one address either.
func (h *handler) applyLimited(w http.ResponseWriter, r *http.Request, userID string) bool {
	if !h.ApplyIP.AllowHTTP(w, httpx.ClientIP(r)) {
		httpx.TooManyRequests(w)
		return true
	}
	if !h.ApplyUser.AllowHTTP(w, userID) {
		httpx.TooManyRequests(w)
		return true
	}
	return false
}
