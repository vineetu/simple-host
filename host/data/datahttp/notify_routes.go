package datahttp

import (
	"encoding/json"
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	notifications "github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
)

// notifyRequester is who a /notifications request is for: the signed-in
// person, also when they act for an account (a team they belong to, or a
// site they edit), whose site the request then names. ok is false (403
// written) when nobody is signed in.
func notifyRequester(w http.ResponseWriter, r *http.Request) (notifications.Requester, bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		notifyError(w, 403, "forbidden")
		return notifications.Requester{}, false
	}
	who := notifications.Requester{Person: user.ID, Account: user.ID, DeployOnly: user.DeployOnly(), Member: user.Member != nil}
	if user.Member != nil {
		who.Person = user.Member.PersonID
	}
	return who, true
}

// notifySubject resolves the site and subscription a request's path names.
func (h *DataHandler) notifySubject(w http.ResponseWriter, r *http.Request) (notifications.Subject, bool) {
	who, ok := notifyRequester(w, r)
	if !ok {
		return notifications.Subject{}, false
	}
	s, err := h.Notifier().Resolve(r.Context(), who, r.PathValue("sitename"), r.PathValue("id"))
	if err != nil {
		writeNotifyError(w, err)
		return s, false
	}
	return s, true
}

// GET /v1/notifications, GET /v1/sites/{sitename}/notifications
func (h *DataHandler) listNotificationsRoute(w http.ResponseWriter, r *http.Request) {
	who, ok := notifyRequester(w, r)
	if !ok {
		return
	}
	views, err := h.Notifier().List(r.Context(), who, r.PathValue("sitename"))
	if err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"notifications": views, "limits": h.Notifier().Limits(h.NotifyEnv())})
}

// POST /v1/sites/{sitename}/notifications
func (h *DataHandler) createNotificationRoute(w http.ResponseWriter, r *http.Request) {
	s, ok := h.notifySubject(w, r)
	if !ok {
		return
	}
	env := h.NotifyEnv()
	if err := notifications.RequireReady(env); err != nil {
		writeNotifyError(w, err)
		return
	}
	var req struct {
		URL        string   `json:"url"`
		Events     []string `json:"events"`
		Storage    string   `json:"storage"`
		Secret     string   `json:"secret"`
		Label      string   `json:"label"`
		AuthHeader struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"auth_header"`
	}
	if !httpx.DecodeJSON(w, r, 16384, &req) {
		return
	}
	n, secret, err := h.Notifier().Create(r.Context(), env, s, notifications.CreateInput{URL: req.URL, Events: req.Events, Storage: req.Storage, Secret: req.Secret, Label: req.Label, HeaderName: req.AuthHeader.Name, HeaderValue: req.AuthHeader.Value})
	if err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"notification": notifications.View(n), "secret": secret})
}

// PATCH /v1/sites/{sitename}/notifications/{id}
func (h *DataHandler) pauseNotificationRoute(w http.ResponseWriter, r *http.Request) {
	s, ok := h.notifySubject(w, r)
	if !ok {
		return
	}
	var req struct {
		Paused *bool `json:"paused"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Paused == nil {
		notifyError(w, 400, "invalid_request")
		return
	}
	n, err := h.Notifier().Pause(r.Context(), s, *req.Paused)
	if err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"notification": notifications.View(n)})
}

// DELETE /v1/sites/{sitename}/notifications/{id}
func (h *DataHandler) deleteNotificationRoute(w http.ResponseWriter, r *http.Request) {
	s, ok := h.notifySubject(w, r)
	if !ok {
		return
	}
	if err := h.Notifier().Delete(r.Context(), s); err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"deleted": true})
}

// POST /v1/sites/{sitename}/notifications/{id}/rotate-secret
func (h *DataHandler) rotateNotificationRoute(w http.ResponseWriter, r *http.Request) {
	s, ok := h.notifySubject(w, r)
	if !ok {
		return
	}
	secret, until, err := h.Notifier().Rotate(r.Context(), h.NotifyEnv(), s)
	if err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"secret": secret, "previous_valid_until": until})
}

// GET /v1/sites/{sitename}/notifications/{id}/deliveries
func (h *DataHandler) notificationDeliveriesRoute(w http.ResponseWriter, r *http.Request) {
	s, ok := h.notifySubject(w, r)
	if !ok {
		return
	}
	deliveries, err := h.Notifier().Deliveries(r.Context(), s)
	if err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deliveries": deliveries})
}

// POST /v1/sites/{sitename}/notifications/{id}/test sends one test event
// through the subscription's real delivery path.
func (h *DataHandler) testNotificationRoute(w http.ResponseWriter, r *http.Request) {
	s, ok := h.notifySubject(w, r)
	if !ok {
		return
	}
	var name, siteURL string
	if s.Site.Live() {
		var err error
		if name, siteURL, err = h.Hosts.NotificationSite(r.Context(), s.Site); err != nil {
			notifyError(w, 500, "internal_error")
			return
		}
	}
	result, err := h.Notifier().Test(r.Context(), h.NotifyEnv(), s, name, siteURL)
	if err != nil {
		writeNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}
