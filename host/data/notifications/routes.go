package notifications

import (
	"encoding/json"
	"net/http"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/httpx"
)

// notifyRequester is who a /notifications request is for: the signed-in
// person, also when they act for an account (a team they belong to, or a
// site they edit), whose site the request then names. ok is false (403
// written) when nobody is signed in.
func notifyRequester(w http.ResponseWriter, r *http.Request) (notify.Requester, bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		data.NotifyError(w, 403, "forbidden")
		return notify.Requester{}, false
	}
	who := notify.Requester{Person: user.ID, Account: user.ID, DeployOnly: user.DeployOnly(), Member: user.Member != nil}
	if user.Member != nil {
		who.Person = user.Member.PersonID
	}
	return who, true
}

// notifySubject resolves the site and subscription a request's path names.
func notifySubject(h *data.Handler, w http.ResponseWriter, r *http.Request) (notify.Subject, bool) {
	who, ok := notifyRequester(w, r)
	if !ok {
		return notify.Subject{}, false
	}
	s, err := h.Notifier().Resolve(r.Context(), who, r.PathValue("sitename"), r.PathValue("id"))
	if err != nil {
		data.WriteNotifyError(w, err)
		return s, false
	}
	return s, true
}

// GET /v1/notifications, GET /v1/sites/{sitename}/notifications
func listNotificationsRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	who, ok := notifyRequester(w, r)
	if !ok {
		return
	}
	body, err := h.Notifier().List(r.Context(), h.NotifyEnv(), who, r.PathValue("sitename"))
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, body)
}

// POST /v1/sites/{sitename}/notifications
func createNotificationRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	s, ok := notifySubject(h, w, r)
	if !ok {
		return
	}
	env := h.NotifyEnv()
	if err := notify.RequireReady(env); err != nil {
		data.WriteNotifyError(w, err)
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
	n, secret, err := h.Notifier().Create(r.Context(), env, s, notify.CreateInput{URL: req.URL, Events: req.Events, Storage: req.Storage, Secret: req.Secret, Label: req.Label, HeaderName: req.AuthHeader.Name, HeaderValue: req.AuthHeader.Value})
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 201, map[string]any{"notification": n, "secret": secret})
}

// PATCH /v1/sites/{sitename}/notifications/{id}
func pauseNotificationRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	s, ok := notifySubject(h, w, r)
	if !ok {
		return
	}
	var req struct {
		Paused *bool `json:"paused"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || req.Paused == nil {
		data.NotifyError(w, 400, "invalid_request")
		return
	}
	n, err := h.Notifier().Pause(r.Context(), s, *req.Paused)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"notification": n})
}

// DELETE /v1/sites/{sitename}/notifications/{id}
func deleteNotificationRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	s, ok := notifySubject(h, w, r)
	if !ok {
		return
	}
	if err := h.Notifier().Delete(r.Context(), s); err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]bool{"deleted": true})
}

// POST /v1/sites/{sitename}/notifications/{id}/rotate-secret
func rotateNotificationRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	s, ok := notifySubject(h, w, r)
	if !ok {
		return
	}
	secret, until, err := h.Notifier().Rotate(r.Context(), h.NotifyEnv(), s)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"secret": secret, "previous_valid_until": until})
}

// GET /v1/sites/{sitename}/notifications/{id}/deliveries
func notificationDeliveriesRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	s, ok := notifySubject(h, w, r)
	if !ok {
		return
	}
	deliveries, err := h.Notifier().Deliveries(r.Context(), s)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deliveries": deliveries})
}

// POST /v1/sites/{sitename}/notifications/{id}/test sends one test event
// through the subscription's real delivery path.
func testNotificationRoute(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	s, ok := notifySubject(h, w, r)
	if !ok {
		return
	}
	result, err := h.Notifier().Test(r.Context(), h.NotifyEnv(), h.Hosts, s)
	if err != nil {
		data.WriteNotifyError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, result)
}

// Register maps the notification routes: the owner's webhooks and
// subscriptions, behind the account and notice middleware.
func Register(mux *http.ServeMux, h *data.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	limiter := h.Notifier().RESTLimiter()
	mux.Handle("GET /v1/notifications", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, listNotificationsRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, listNotificationsRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications", authMiddleware(httpx.RateLimitByIP(limiter, h.CreateIdempotency.Wrap(noticeMiddleware(bind(h, createNotificationRoute))))))
	mux.Handle("PATCH /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, pauseNotificationRoute)))))
	mux.Handle("DELETE /v1/sites/{sitename}/notifications/{id}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, deleteNotificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/test", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, testNotificationRoute)))))
	mux.Handle("POST /v1/sites/{sitename}/notifications/{id}/rotate-secret", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, rotateNotificationRoute)))))
	mux.Handle("GET /v1/sites/{sitename}/notifications/{id}/deliveries", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(limiter, bind(h, notificationDeliveriesRoute)))))
}

// bind is route as a handler of h's requests.
func bind(h *data.Handler, route func(*data.Handler, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { route(h, w, r) }
}
