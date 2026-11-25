// Package notify tells a site's owner what they asked to be told: saved data
// changed in a table or file store they picked, or someone asked for access.
// Only the owner (or the owning team's members) is told, two ways: the AI
// app's event stream (GET /mcp) and browser push on their devices. Features
// write event rows (db.Event); this package reads and delivers them.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"simplehost/app"
	"simplehost/core/limits"
	"simplehost/core/web"
)

type handler struct{ a *app.App }

// Mount adds routes 14 and 15 and the AI app's event stream (GET /mcp).
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/me/push", web.Func(h.addDevice))
	a.Routes.Platform.Handle("DELETE /v1/me/push/{id}", web.Func(h.removeDevice))
	a.Routes.Platform.Handle("GET /mcp", web.Func(h.stream))
}

// Rule is one thing a site's owner wants to be told about: kind "data" with
// the namespace and target (a table, "_values" or "_files"), or "access".
type Rule struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Target    string `json:"target,omitempty"`
}

// Set replaces what the site's owner is told about. An empty list turns
// every notification off.
func Set(ctx context.Context, a *app.App, siteID string, rules []Rule) error {
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM notify_rules WHERE site_id = $1`, siteID); err != nil {
		return err
	}
	for _, r := range rules {
		if r.Kind == "access" {
			r.Namespace, r.Target = "", ""
		} else if r.Kind != "data" || r.Namespace == "" || r.Target == "" {
			return web.Refuse(http.StatusBadRequest, "bad_notification", `Give each one as {"kind": "access"} or {"kind": "data", "namespace": "...", "target": "<table, _values or _files>"}.`)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notify_rules (site_id, kind, namespace, target) VALUES ($1, $2, $3, $4)`, siteID, r.Kind, r.Namespace, r.Target); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Rules is what the site's owner is told about.
func Rules(ctx context.Context, a *app.App, siteID string) ([]Rule, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT kind, namespace, target FROM notify_rules WHERE site_id = $1 ORDER BY kind, namespace, target`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Rule{}
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.Kind, &r.Namespace, &r.Target); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Device is one browser registered for push.
type Device struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created_at"`
}

// Devices is a person's browsers registered for push.
func Devices(ctx context.Context, a *app.App, personID string) ([]Device, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, name, created_at FROM push_devices WHERE person_id = $1 ORDER BY created_at`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.Created); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func signedIn(r *http.Request) (web.Caller, error) {
	c := web.CallerOf(r)
	if c.ID == "" {
		return c, web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in, or send your key as X-API-Key: <key>.")
	}
	return c, nil
}

// addDevice is route 14: {endpoint, keys: {p256dh, auth}, name}.
func (h *handler) addDevice(w http.ResponseWriter, r *http.Request) error {
	c, err := signedIn(r)
	if err != nil {
		return err
	}
	var in struct {
		Endpoint string `json:"endpoint"`
		Name     string `json:"name"`
		Keys     struct{ P256dh, Auth string }
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		return web.Refuse(http.StatusBadRequest, "bad_push", "Send the browser's push subscription: its https endpoint and its p256dh and auth keys.")
	}
	var n int
	if err := h.a.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM push_devices WHERE person_id = $1 AND endpoint <> $2`, c.ID, in.Endpoint).Scan(&n); err != nil {
		return err
	}
	if int64(n) >= limits.PushDevices.N {
		return web.Refuse(http.StatusConflict, "limit_"+limits.PushDevices.Name, limits.PushDevices.Fix)
	}
	var id string
	err = h.a.DB.QueryRowContext(r.Context(), `INSERT INTO push_devices (person_id, endpoint, name, p256dh, auth) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (person_id, endpoint) DO UPDATE SET name = $3, p256dh = $4, auth = $5 RETURNING id`, c.ID, in.Endpoint, in.Name, in.Keys.P256dh, in.Keys.Auth).Scan(&id)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]string{"id": id})
	return nil
}

// removeDevice is route 15.
func (h *handler) removeDevice(w http.ResponseWriter, r *http.Request) error {
	c, err := signedIn(r)
	if err != nil {
		return err
	}
	res, err := h.a.DB.ExecContext(r.Context(), `DELETE FROM push_devices WHERE id::text = $1 AND person_id = $2`, r.PathValue("id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	web.JSON(w, http.StatusOK, map[string]bool{"removed": true})
	return nil
}

// Event is one thing the owner is told.
type Event struct {
	ID   int64           `json:"id"`
	At   time.Time       `json:"at"`
	Kind string          `json:"kind"`
	Body json.RawMessage `json:"body"`
}

// told is the condition for an event the owner asked to be told about: data
// events are written only when watched; access events only count when the
// site's owner switched them on.
const told = `(e.kind <> 'access' OR EXISTS (SELECT 1 FROM notify_rules n WHERE n.site_id = e.site_id AND n.kind = 'access'))`

// Feed is the events after id for a person: their own sites' and their
// teams' sites'.
func Feed(ctx context.Context, a *app.App, personID string, after int64) ([]Event, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT e.id, e.at, e.kind, e.body FROM events e
		WHERE e.id > $2 AND (e.owner_id = $1 OR e.owner_id IN (SELECT team_id FROM team_members WHERE person_id = $1)) AND `+told+`
		ORDER BY e.id LIMIT 100`, personID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.At, &e.Kind, &e.Body); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// stream is the AI app's event stream (MCP, GET /mcp): each event as a
// notifications/message, with its id so a reconnect (Last-Event-ID) misses
// nothing. Without Last-Event-ID it starts from now.
func (h *handler) stream(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="https://`+h.a.Config.AppDomain+`/.well-known/oauth-protected-resource/mcp"`)
		return web.Refuse(http.StatusUnauthorized, "sign_in", "Connect the app to Simple Host first.")
	}
	after, err := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if err != nil {
		if err := h.a.DB.QueryRowContext(r.Context(), `SELECT COALESCE(max(id), 0) FROM events`).Scan(&after); err != nil {
			return err
		}
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if flusher != nil {
		flusher.Flush()
	}
	for {
		events, err := Feed(r.Context(), h.a, c.ID, after)
		if err != nil {
			return nil // the stream ends; the app reconnects with Last-Event-ID
		}
		for _, e := range events {
			msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/message",
				"params": map[string]any{"level": "info", "logger": "simple-host", "data": e}})
			fmt.Fprintf(w, "id: %d\nevent: message\ndata: %s\n\n", e.ID, msg)
			after = e.ID
		}
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// Work delivers browser pushes every few seconds until ctx ends.
func Work(ctx context.Context, a *app.App) {
	for {
		if err := Deliver(ctx, a); err != nil && ctx.Err() == nil {
			log.Printf("push: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// Deliver pushes every event not yet pushed to the devices of the people it
// is for. A push that fails is not retried; a device the push service says
// is gone is removed. Events nobody asked for are only marked.
func Deliver(ctx context.Context, a *app.App) error {
	key, err := vapid(a.Config.VAPIDKey)
	if err != nil {
		return err
	}
	rows, err := a.DB.QueryContext(ctx, `SELECT e.id, e.kind, e.owner_id, e.body, `+told+` FROM events e
		WHERE e.pushed_at IS NULL ORDER BY e.id LIMIT 100`)
	if err != nil {
		return err
	}
	type event struct {
		id          int64
		kind, owner string
		body        map[string]string
		tell        bool
	}
	var events []event
	for rows.Next() {
		var e event
		var raw []byte
		if err := rows.Scan(&e.id, &e.kind, &e.owner, &raw, &e.tell); err != nil {
			rows.Close()
			return err
		}
		json.Unmarshal(raw, &e.body)
		events = append(events, e)
	}
	rows.Close()
	for _, e := range events {
		if e.tell && key != nil {
			owner, _ := a.People.Get(ctx, e.owner)
			site := e.body["site"]
			msg := map[string]string{"title": "Someone asked for access to " + site, "body": e.body["email"] + " wants to open the site.",
				"url": "/dashboard/sites/" + owner.Handle + "/" + site, "tag": "sh-" + strconv.FormatInt(e.id, 10)}
			if e.kind == "data" {
				msg["title"], msg["body"] = "New "+e.body["action"]+" on "+site, "In "+e.body["namespace"]+" / "+e.body["target"]+"."
			}
			if err := pushAll(ctx, a, key, e.owner, msg); err != nil {
				return err
			}
		}
		if _, err := a.DB.ExecContext(ctx, `UPDATE events SET pushed_at = now() WHERE id = $1`, e.id); err != nil {
			return err
		}
	}
	return nil
}

// pushAll sends msg to every device of the owner, or of every member when the
// owner is a team.
func pushAll(ctx context.Context, a *app.App, key *signer, ownerID string, msg map[string]string) error {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, endpoint, p256dh, auth FROM push_devices
		WHERE person_id = $1 OR person_id IN (SELECT person_id FROM team_members WHERE team_id = $1)`, ownerID)
	if err != nil {
		return err
	}
	var devices [][4]string
	for rows.Next() {
		var d [4]string
		if err := rows.Scan(&d[0], &d[1], &d[2], &d[3]); err != nil {
			rows.Close()
			return err
		}
		devices = append(devices, d)
	}
	rows.Close()
	payload, _ := json.Marshal(msg)
	for _, d := range devices {
		status, err := key.send(ctx, a.Guarded, d[1], d[2], d[3], payload)
		if ue := (*url.Error)(nil); errors.As(err, &ue) {
			err = ue.Err // the endpoint is the device's secret address: never in the log
		}
		if err != nil {
			log.Printf("push to a device: %v", err)
		}
		if status == http.StatusNotFound || status == http.StatusGone {
			a.DB.ExecContext(ctx, `DELETE FROM push_devices WHERE id = $1`, d[0])
		}
	}
	return nil
}
