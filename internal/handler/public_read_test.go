package handler

import (
	"net/http"
	"testing"
)

// Saved state and public lists are public to read. A read that names no page
// (no Origin, no Referer: curl, an agent) is served; a read from a page is
// still Origin-checked, so another site's page cannot read with CORS. Needs
// DB_DSN (db/schema.sql applied).
func TestPublicReadsWithoutOrigin(t *testing.T) {
	a := newPersonApp(t, "off")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "board")
	const apex = pcSiteDomain
	page := map[string]string{"Origin": "https://" + pcContentHost}
	write := map[string]string{"X-API-Key": olive.key, "Origin": "https://" + pcContentHost}

	if r := a.at(t, "PUT", apex, "/v1/sites/board/state", map[string]any{"votes": 3}, write); r.status != http.StatusOK {
		t.Fatalf("seed state: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/board/collections/notes", map[string]any{"msg": "hi"}, write); r.status != http.StatusCreated {
		t.Fatalf("seed list: %d %s", r.status, r.body)
	}

	// No Origin, no Referer: public, served.
	r := a.at(t, "GET", apex, "/v1/sites/board/state", nil, nil)
	if r.status != http.StatusOK || r.json(t)["votes"] != float64(3) {
		t.Fatalf("originless state read: %d %s", r.status, r.body)
	}
	if r.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("originless read must not grant CORS: %q", r.header.Get("Access-Control-Allow-Origin"))
	}
	if r := a.at(t, "GET", apex, "/v1/sites/board/collections/notes", nil, nil); r.status != http.StatusOK || len(r.json(t)["items"].([]any)) != 1 {
		t.Fatalf("originless list read: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites/nope-missing/state", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("missing site: %d %s", r.status, r.body)
	}

	// The site's own page still reads, with CORS.
	if r := a.at(t, "GET", apex, "/v1/sites/board/state", nil, page); r.status != http.StatusOK || r.header.Get("Access-Control-Allow-Origin") != page["Origin"] {
		t.Fatalf("own-page read: %d %q", r.status, r.header.Get("Access-Control-Allow-Origin"))
	}

	// Another origin, by Origin or by Referer: still refused.
	for _, h := range []map[string]string{
		{"Origin": "https://evil.example"},
		{"Referer": "https://evil.example/x"},
		{"Origin": "null"},
	} {
		if r := a.at(t, "GET", apex, "/v1/sites/board/state", nil, h); r.status != http.StatusForbidden {
			t.Fatalf("foreign state read %v: %d", h, r.status)
		}
		if r := a.at(t, "GET", apex, "/v1/sites/board/collections/notes", nil, h); r.status != http.StatusForbidden {
			t.Fatalf("foreign list read %v: %d", h, r.status)
		}
	}

	// Writes are unchanged: no Origin, no write, even with a key.
	if r := a.at(t, "PUT", apex, "/v1/sites/board/state", map[string]any{"votes": 0}, map[string]string{"X-API-Key": olive.key}); r.status != http.StatusForbidden {
		t.Fatalf("originless write: %d %s", r.status, r.body)
	}
}
