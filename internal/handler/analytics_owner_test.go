package handler

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ?owner=<handle> on the per-site analytics endpoints: the platform admin may
// read any account's site; everyone else gets exactly the 404 of a site that
// does not exist. Needs DB_DSN (db/schema.sql applied).
func TestSiteAnalyticsOwnerParam(t *testing.T) {
	a := newPrivateApp(t)
	olive, oscar, boss := a.newPerson(t, "olive"), a.newPerson(t, "oscar"), a.newPerson(t, "boss")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "shop")
	a.deploy(t, oscar, "oscar-only")
	_, oliveHandle := a.userID(t, olive)
	_, oscarHandle := a.userID(t, oscar)
	bossID, _ := a.userID(t, boss)
	if _, err := a.database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, bossID); err != nil {
		t.Fatal(err)
	}

	const apex = "simple-host.test"
	get := func(key, path string) resp {
		h := map[string]string{}
		if key != "" {
			h["X-API-Key"] = key
		}
		return a.at(t, "GET", apex, path, nil, h)
	}
	paths := func(site, owner string) []string {
		q := "?days=7"
		if owner != "" {
			q += "&owner=" + url.QueryEscape(owner)
		}
		return []string{"/v1/sites/" + site + "/analytics" + q, "/v1/sites/" + site + "/analytics/geo" + q}
	}
	want := func(label, key string, code int, site, owner string) []resp {
		t.Helper()
		var out []resp
		for _, p := range paths(site, owner) {
			r := get(key, p)
			if r.status != code {
				t.Errorf("%s: GET %s = %d, want %d (%s)", label, p, r.status, code, r.body)
			}
			out = append(out, r)
		}
		return out
	}

	// Unauthenticated: rejected whatever owner says.
	want("no key", "", http.StatusUnauthorized, "shop", "")
	want("no key + owner", "", http.StatusUnauthorized, "shop", oliveHandle)

	// Owner reading their own site: unchanged, with or without their own handle.
	want("own site", olive.key, http.StatusOK, "shop", "")
	want("own site + own handle", olive.key, http.StatusOK, "shop", oliveHandle)
	want("own site + own handle, other case", olive.key, http.StatusOK, "shop", strings.ToUpper(oliveHandle))

	// Non-admin naming another owner: indistinguishable from a missing site.
	missing := want("missing site", olive.key, http.StatusNotFound, "no-such-site", "")
	for _, c := range []struct{ label, site, owner string }{
		{"other owner, existing site", "oscar-only", oscarHandle},
		{"other owner, same-named site", "shop", oscarHandle},
		{"unknown owner", "shop", "no-such-handle-xyz"},
		{"other owner, missing site", "no-such-site", oscarHandle},
	} {
		got := want("non-admin "+c.label, olive.key, http.StatusNotFound, c.site, c.owner)
		for i := range got {
			if !bytes.Equal(got[i].body, missing[i].body) {
				t.Errorf("non-admin %s: body %s differs from missing-site body %s", c.label, got[i].body, missing[i].body)
			}
		}
	}

	// Admin (both the ADMIN_API_KEY identity and an is_admin account).
	for _, adm := range []struct{ label, key string }{{"admin key", a.admin}, {"is_admin account", boss.key}} {
		want(adm.label+" reads other owner", adm.key, http.StatusOK, "oscar-only", oscarHandle)
		want(adm.label+" reads same-named site of other owner", adm.key, http.StatusOK, "shop", oliveHandle)
		// The owner really scopes the lookup: olive has no oscar-only.
		want(adm.label+" wrong owner for site", adm.key, http.StatusNotFound, "oscar-only", oliveHandle)
		want(adm.label+" unknown owner", adm.key, http.StatusNotFound, "shop", "no-such-handle-xyz")
		// Without owner the admin sees only their own sites, as before.
		want(adm.label+" no owner", adm.key, http.StatusNotFound, "oscar-only", "")
	}
}
