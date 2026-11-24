// Package rules is route 32: the owner sets who may read, add, change and
// delete each table, the values (_values) and the file records (_files) of a
// namespace, and its spam check keys. A mistake is reported exactly when the
// rules are saved. No rules for a table means only the owner touches it.
package rules

import (
	"encoding/json"
	"net/http"
	"sort"

	"simplehost/app"
	"simplehost/core/web"
	data "simplehost/data/core"
)

type handler struct{ a *app.App }

// Mount adds the rules route on the platform.
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("PUT /v1/sites/{site}/storage/{ns}/rules", web.Func(h.set))
}

// set replaces the namespace's rules (making the namespace if new). The
// Turnstile keys are kept unless sent; the secret is never shown.
func (h *handler) set(w http.ResponseWriter, r *http.Request) error {
	site, err := h.a.Sites.Of(r)
	if err == nil {
		err = h.a.Sites.Manage(r, site)
	}
	if err == nil {
		err = data.Unforged(r)
	}
	if err != nil {
		return err
	}
	var in struct {
		Rules           map[string]json.RawMessage `json:"rules"`
		TurnstileKey    *string                    `json:"turnstile_key"`
		TurnstileSecret *string                    `json:"turnstile_secret"`
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	name := r.PathValue("ns")
	if err := h.a.Data.Create(r.Context(), site, name); err != nil {
		return err
	}
	ns, err := h.a.Data.Open(r, site, name)
	if err != nil {
		return err
	}
	defer ns.Close()
	if in.TurnstileKey != nil {
		ns.TurnstileKey = *in.TurnstileKey
	}
	if in.TurnstileSecret != nil {
		ns.TurnstileSecret = *in.TurnstileSecret
	}
	if in.Rules == nil {
		in.Rules = map[string]json.RawMessage{}
	}
	ns.Rules = in.Rules
	tables := make([]string, 0, len(in.Rules))
	for t := range in.Rules {
		tables = append(tables, t)
	}
	sort.Strings(tables)
	for _, t := range tables {
		have, err := ns.Query(r.Context(), `SELECT count(*) FROM pragma_table_info(?)`, t)
		if err != nil {
			return err
		}
		if have.Rows[0][0].(int64) == 0 {
			return web.Refuse(http.StatusBadRequest, "no_table", "The namespace has no table named "+t+". Make it with SQL first, then set its rules.")
		}
		if _, err := ns.Plan(r.Context(), t); err != nil {
			return err
		}
	}
	saved, _ := json.Marshal(in.Rules)
	_, err = h.a.DB.ExecContext(r.Context(), `UPDATE namespaces SET rules = $3, turnstile_key = $4, turnstile_secret = $5 WHERE site_id = $1 AND name = $2`,
		site.ID, name, saved, ns.TurnstileKey, ns.TurnstileSecret)
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"namespace": name, "rules": in.Rules, "spam_check": ns.TurnstileSecret != ""})
	return nil
}
