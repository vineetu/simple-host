package handler

import (
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
)

// Owner visit marks (SIM-151, 2026-11-02; host/app/operations/ownermarks.go).
// The server notes the address an account is seen signed in from so the
// ingester can file that account's own visits to its sites as class 'owner'
// and leave them out of People. Best effort throughout: a failed mark is
// logged and never changes a response.

// SetOwnerMarks turns mark writing on. Left unset (an install without the
// analytics log) nothing is written.
func (h *SiteHandler) SetOwnerMarks(m *operations.OwnerMarks) { h.ownerMarks = m }

// noteOwnerBrowser marks the caller's address when the request is the owner's
// own browser on the apex: the dashboard and the owner app fetch the site
// list and the analytics reads same-origin, and browsers set Sec-Fetch-Site:
// same-origin on those fetches themselves (a page cannot forge it). curl,
// SDKs and the connector's internal calls never send it, so an agent reading
// the list on the owner's behalf from some other machine marks nothing.
func (h *SiteHandler) noteOwnerBrowser(r *http.Request, user *model.User) {
	if h.ownerMarks == nil || user == nil {
		return
	}
	if !strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "same-origin") {
		return
	}
	if err := h.ownerMarks.Mark(r.Context(), user.ID, clientIP(r)); err != nil {
		log.Printf("owner marks: browser user=%s: %v", user.ID, err)
	}
}

// visitorSignedIn is the one place a visitor sign-in is noted, after the
// session row is written: the first one on a site sends visitor.signed_up,
// and the owner's own sign-in marks the caller's address. It never affects
// the response.
func (h *SiteHandler) visitorSignedIn(r *http.Request, siteID, userID string, first bool) {
	if first {
		h.data.emitSiteNotification(siteID, "visitor.signed_up", map[string]any{"visitor": userID}, 0)
	}
	if h.ownerMarks == nil || siteID == "" || userID == "" {
		return
	}
	h.ownerMarkSvc().NoteOwnerVisitorSignIn(r.Context(), sites.OwnerMarkInput{SiteID: siteID, UserID: userID, IP: clientIP(r)})
}

func (h *SiteHandler) ownerMarkSvc() *sites.Service {
	return sites.New(h.database, nil, nil, 0, sites.Deps{OwnerMarks: sites.OwnerMarkDeps{Mark: h.ownerMarks.Mark}})
}
