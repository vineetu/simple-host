package handler

import (
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/operations"
	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/model"
)

// Owner visit marks (SIM-151, 2026-11-02; internal/app/operations/ownermarks.go).
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
// the list on the owner's behalf from some other machine marks nothing. A
// team credential is the team's, not a person's, and is never marked.
func (h *SiteHandler) noteOwnerBrowser(r *http.Request, user *model.User) {
	if h.ownerMarks == nil || user == nil || user.Team != nil {
		return
	}
	if !strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "same-origin") {
		return
	}
	if err := h.ownerMarks.Mark(r.Context(), user.ID, clientIP(r)); err != nil {
		log.Printf("owner marks: browser user=%s: %v", user.ID, err)
	}
}

// noteOwnerVisitorSignIn marks the caller's address when a visitor has just
// signed in on a site with the account that owns it. Called after the
// session row is written; it never affects the response.
func (h *SiteHandler) noteOwnerVisitorSignIn(r *http.Request, siteID, userID string) {
	if h.ownerMarks == nil || siteID == "" || userID == "" {
		return
	}
	h.ownerMarkSvc().NoteOwnerVisitorSignIn(r.Context(), sites.OwnerMarkInput{SiteID: siteID, UserID: userID, IP: clientIP(r)})
}
