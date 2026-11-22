package sitesroute

import (
	"log"
	"net/http"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
)

// Owner visit marks.
// The server notes the address an account is seen signed in from so the
// ingester can file that account's own visits to its sites as class 'owner'
// and leave them out of People. Best effort throughout: a failed mark is
// logged and never changes a response.

// SetOwnerMarks turns mark writing on; unset (no analytics log) nothing is written.
func (h *Handler) SetOwnerMarks(m *operations.OwnerMarks) {
	h.ownerMarks = m
}

// SetVisitorSignIn records whether a visitor can sign in on this install
// (the named-viewers refusal on create reads it).
func (h *Handler) SetVisitorSignIn(email bool, providers []string) {
	h.noVisitorSignIn = accounts.NoVisitorSignIn(email, providers)
}

// noteOwnerBrowser marks the caller's address when the request is the owner's
// own browser on the apex: the dashboard and the owner app fetch the site
// list and the analytics reads same-origin, and browsers set Sec-Fetch-Site:
// same-origin on those fetches themselves (a page cannot forge it). curl,
// SDKs and the connector's internal calls never send it, so an agent reading
// the list on the owner's behalf from some other machine marks nothing.
func (h *Handler) NoteOwnerBrowser(r *http.Request, user *model.User) {
	if h.ownerMarks == nil || user == nil {
		return
	}
	if !strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "same-origin") {
		return
	}
	if err := h.ownerMarks.Mark(r.Context(), user.ID, httpx.ClientIP(r)); err != nil {
		log.Printf("owner marks: browser user=%s: %v", user.ID, err)
	}
}
