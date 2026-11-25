// Package keys is API keys (F4): one kind of key, made in the dashboard and
// shown once, to use Simple Host without a connector, for anything the
// person can do. A key acts as the person who made it.
package keys

import (
	"context"
	"net/http"
	"strings"
	"time"

	"simplehost/app"
	"simplehost/core/db"
	"simplehost/core/web"
)

type handler struct{ a *app.App }

// Mount adds routes 11 and 12.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/keys", web.Func(h.make))
	a.Routes.Platform.Handle("DELETE /v1/keys/{id}", web.Func(h.revoke))
}

var errSignIn = web.Refuse(http.StatusUnauthorized, "sign_in", "Sign in to the dashboard first.")

// make is route 11: a named key, shown this once.
func (h *handler) make(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	var in struct{ Name string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 60 {
		return web.Refuse(http.StatusBadRequest, "bad_key_name", "Give the key a name of 1 to 60 characters, like \"deploy script\".")
	}
	id, key, err := h.a.People.NewKey(r.Context(), c.ID, name)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusCreated, map[string]string{"id": id, "name": name, "key": key, "last4": key[len(key)-4:]})
	return nil
}

// revoke is route 12: the key stops working at once.
func (h *handler) revoke(w http.ResponseWriter, r *http.Request) error {
	c := web.CallerOf(r)
	if c.ID == "" {
		return errSignIn
	}
	res, err := h.a.DB.ExecContext(r.Context(), `DELETE FROM keys WHERE id::text = $1 AND person_id = $2`, r.PathValue("id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return web.NotFound
	}
	web.JSON(w, http.StatusOK, map[string]bool{"revoked": true})
	return nil
}

// Key is what the dashboard shows of a key: never the key itself.
type Key struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Last4    string     `json:"last4"`
	Created  time.Time  `json:"created_at"`
	LastUsed *time.Time `json:"last_used_at"`
}

// List is a person's keys, newest first (for GET /v1/me).
func List(ctx context.Context, q db.Q, personID string) ([]Key, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, name, last4, created_at, last_used_at FROM keys WHERE person_id = $1 ORDER BY created_at DESC`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Key{}
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.ID, &k.Name, &k.Last4, &k.Created, &k.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
