package handler

import (
	"net/http"
	"strings"
	"testing"
)

// C2: a list made public after it was private still holds the submitters'
// emails. Only the owner (key, or signed in on the site's own address) and
// the admin see _submitted_by; every other read (a page, a stranger's key or
// connector, no key at all) gets the items without it.
func TestPublicListHidesSubmitters(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	olive, vic := a.newPerson(t, "olive"), a.newPerson(t, "vic")
	a.deploy(t, olive, "shop")
	_, oh := a.userID(t, olive)
	shopID := a.siteID(t, olive, "shop")
	const apex = pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	claimed := oh + "-store." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`INSERT INTO collection_items (site_id, collection, data) VALUES
		($1, 'orders', '{"name":"Ana","_submitted_by":"ana@example.com","_submitted_at":"2026-09-24T18:02:11Z"}'),
		($1, 'orders', '{"name":"Bo"}')`, shopID); err != nil {
		t.Fatal(err)
	}
	path := "/v1/u/" + oh + "/sites/shop/collections/orders"
	read := func(name, host string, headers map[string]string) string {
		t.Helper()
		r := a.at(t, "GET", host, path, nil, headers)
		if r.status != 200 || len(itemsOf(t, r)) != 2 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
		return string(r.body)
	}
	for name, tc := range map[string]struct {
		host    string
		headers map[string]string
	}{
		"no key, no page":          {apex, nil},
		"page on the site":         {claimed, map[string]string{"Origin": "https://" + claimed}},
		"stranger's key":           {apex, map[string]string{"X-API-Key": vic.key}},
		"stranger signed in there": {claimed, browser(claimed, a.session(t, vic, shopID, claimed))},
	} {
		body := read(name, tc.host, tc.headers)
		if strings.Contains(body, "ana@example.com") || strings.Contains(body, "_submitted_by") {
			t.Errorf("%s sees the submitter: %s", name, body)
		}
		if !strings.Contains(body, "_submitted_at") || !strings.Contains(body, `"Ana"`) {
			t.Errorf("%s lost the rest of the item: %s", name, body)
		}
	}
	for name, tc := range map[string]struct {
		host    string
		headers map[string]string
	}{
		"owner's key":           {apex, okey},
		"admin key":             {apex, map[string]string{"X-API-Key": a.admin}},
		"owner signed in there": {claimed, browser(claimed, a.session(t, olive, shopID, claimed))},
	} {
		if body := read(name, tc.host, tc.headers); !strings.Contains(body, "ana@example.com") {
			t.Errorf("%s does not see the submitter: %s", name, body)
		}
	}
	// The spreadsheet is the owner's alone and keeps the column.
	if r := a.at(t, "GET", apex, "/v1/sites/shop/collections/orders/export.csv", nil, okey); r.status != 200 || !strings.Contains(string(r.body), "ana@example.com") {
		t.Errorf("owner CSV: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites/shop/collections/orders/export.csv", nil, map[string]string{"X-API-Key": vic.key}); r.status != http.StatusNotFound {
		t.Errorf("stranger CSV: %d %s", r.status, r.body)
	}
}
