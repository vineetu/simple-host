package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 2: kinds (kinds.go). Needs DB_DSN (db/schema.sql applied).

// kindsSite is newSavedDataSite with a second visitor (wes). legacy=false
// makes it strict: a site made after the kinds (legacy_data false) on an
// install with SAVED_DATA_DEFAULT_KIND=declare_first. legacy=true is a site
// from before the kinds (the default install makes every site behave so).
type kindsSite struct {
	*savedDataSite
	wes      person
	wesCooky string
	strict   bool
}

func newKindsSite(t *testing.T, legacy bool) *kindsSite {
	t.Helper()
	s := &kindsSite{savedDataSite: newSavedDataSite(t), strict: !legacy}
	if _, err := s.a.database.Exec(`UPDATE sites SET legacy_data = $2 WHERE id = $1`, s.shopID, legacy); err != nil {
		t.Fatal(err)
	}
	s.setLimits(t, func(*config.SavedData) {})
	s.wes = s.a.newPerson(t, "wes")
	s.wesCooky = s.a.session(t, s.wes, s.shopID, s.dom)
	return s
}

// as is a browser request from the site's own address with a visitor cookie.
func (s *kindsSite) as(t *testing.T, cookie, method, path string, body any) resp {
	t.Helper()
	return s.a.at(t, method, s.dom, path, body, browser(s.dom, cookie))
}

// ownerPage is an owner request with the key and the site's page as Origin
// (as the connector sends it: the list routes gate on the Origin).
func (s *kindsSite) ownerPage(t *testing.T, method, path string, body any) resp {
	t.Helper()
	return s.a.at(t, method, pcSiteDomain, path, body, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + s.dom})
}

func (s *kindsSite) declare(t *testing.T, name string, body map[string]any) map[string]any {
	t.Helper()
	r := s.owner(t, "PUT", "/v1/sites/shop/data/"+name+"/kind", body)
	if r.status != 200 {
		t.Fatalf("declare %s %v: %d %s", name, body, r.status, r.body)
	}
	return r.json(t)
}

func (s *kindsSite) setLimits(t *testing.T, edit func(*config.SavedData)) {
	t.Helper()
	c := config.DefaultSavedData()
	if s.strict {
		c.DefaultKind = config.DefaultKindDeclareFirst
	}
	edit(&c)
	s.a.sites.SetSavedData(c)
	t.Cleanup(func() { s.a.sites.SetSavedData(config.DefaultSavedData()) })
}

func wantCode(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: %d %s, want %d %s", what, r.status, r.body, status, code)
	}
	if code != "" {
		var body struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(r.body, &body)
		if body.Code != code {
			t.Fatalf("%s: code %q (%s), want %q", what, body.Code, r.body, code)
		}
	}
}

func idOf(t *testing.T, r resp) string {
	t.Helper()
	var it struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.body, &it); err != nil || it.ID == 0 {
		t.Fatalf("no id: %d %s", r.status, r.body)
	}
	return strconv.FormatInt(it.ID, 10)
}

func TestDeclareFirstOnNewSites(t *testing.T) {
	s := newKindsSite(t, false)
	wantCode(t, "owner append, undeclared", s.ownerPage(t, "POST", "/v1/sites/shop/collections/rsvps", map[string]any{"a": 1}), 409, "declare_first")
	wantCode(t, "visitor append, undeclared", s.visitor(t, "POST", "/v1/sites/shop/data/rsvps", map[string]any{"a": 1}), 409, "declare_first")
	wantCode(t, "owner page info, undeclared", s.owner(t, "PUT", "/v1/sites/shop/data/menu", map[string]any{"a": 1}), 409, "declare_first")
	wantCode(t, "kind, undeclared", s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/rsvps/kind", nil, nil), 200, "")
	if k := s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/rsvps/kind", nil, nil).json(t); k["declared"] != false || k["accepts_saves"] != false {
		t.Fatalf("kind of an undeclared name on a new site: %v", k)
	}
	// Page data (state) is untouched by step 2.
	if r := s.visitor(t, "PATCH", "/v1/sites/shop/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n"}}}); r.status != 200 {
		t.Fatalf("state patch: %d %s", r.status, r.body)
	}
	// Once declared, it takes saves.
	s.declare(t, "rsvps", map[string]any{"kind": "entries"})
	if r := s.visitor(t, "POST", "/v1/sites/shop/data/rsvps", map[string]any{"name": "Vic"}); r.status != 201 {
		t.Fatalf("after declaring: %d %s", r.status, r.body)
	}
	// A site deployed through the API is made after the kinds, so on this
	// install it is strict.
	s.a.deploy(t, s.olive, "fresh")
	var legacy bool
	if err := s.a.database.QueryRow(`SELECT legacy_data FROM sites WHERE id = $1`, s.a.siteID(t, s.olive, "fresh")).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy {
		t.Fatal("a new site was created with legacy_data")
	}
	wantCode(t, "fresh site, undeclared", s.a.at(t, "POST", pcSiteDomain, "/v1/sites/fresh/collections/rsvps", map[string]any{"a": 1}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + pcContentHost}), 409, "declare_first")
}

func TestLegacySiteUndeclaredUnchanged(t *testing.T) {
	s := newKindsSite(t, true)
	// A list nobody declared behaves as before: public, anyone signed in adds.
	r := s.visitor(t, "POST", "/v1/sites/shop/collections/guestbook", map[string]any{"msg": "hi"})
	if r.status != 201 {
		t.Fatalf("legacy append: %d %s", r.status, r.body)
	}
	if items := itemsOf(t, s.a.at(t, "GET", s.dom, "/v1/sites/shop/collections/guestbook", nil, nil)); len(items) != 1 {
		t.Fatalf("public read: %v", items)
	}
	if k := s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/guestbook/kind", nil, nil).json(t); k["declared"] != false || k["accepts_saves"] != true || k["visibility"] != "public" {
		t.Fatalf("legacy kind answer: %v", k)
	}
	// The same list through the new route, and a visitor's own entry (added).
	id := idOf(t, r)
	if r := s.visitor(t, "PATCH", "/v1/sites/shop/data/guestbook/items/"+id, map[string]any{"msg": "hello"}); r.status != 200 {
		t.Fatalf("own edit on a legacy list: %d %s", r.status, r.body)
	}
	if r := s.as(t, s.wesCooky, "PATCH", "/v1/sites/shop/data/guestbook/items/"+id, map[string]any{"msg": "mine now"}); r.status != 404 {
		t.Fatalf("someone else's edit: %d %s", r.status, r.body)
	}
	// Page info must still be declared, legacy site or not.
	wantCode(t, "put undeclared", s.owner(t, "PUT", "/v1/sites/shop/data/menu", map[string]any{"a": 1}), 409, "declare_first")
}

func TestPageInfoRules(t *testing.T) {
	s := newKindsSite(t, false)
	out := s.declare(t, "menu", map[string]any{"kind": "Page info"})
	if out["kind"] != "content" || out["label"] != "Page info" {
		t.Fatalf("declare: %v", out)
	}
	if r := s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/menu", nil, nil); r.status != 200 || r.json(t)["data"] != nil {
		t.Fatalf("empty read: %d %s", r.status, r.body)
	}
	wantCode(t, "visitor put", s.visitor(t, "PUT", "/v1/sites/shop/data/menu", map[string]any{"x": 1}), 403, "owner_only")
	wantCode(t, "visitor post", s.visitor(t, "POST", "/v1/sites/shop/data/menu", map[string]any{"x": 1}), 409, "wrong_kind")
	wantCode(t, "visitor post, old route", s.visitor(t, "POST", "/v1/sites/shop/collections/menu", map[string]any{"x": 1}), 409, "wrong_kind")
	stranger := s.a.newPerson(t, "stranger")
	wantCode(t, "another account's key", s.a.at(t, "PUT", pcSiteDomain, "/v1/sites/shop/data/menu", map[string]any{"x": 1}, map[string]string{"X-API-Key": stranger.key}), 404, "")
	wantCode(t, "not an object", s.owner(t, "PUT", "/v1/sites/shop/data/menu", []any{1, 2}), 400, "not_an_object")
	if r := s.owner(t, "PUT", "/v1/sites/shop/data/menu", map[string]any{"soup": 4}); r.status != 200 {
		t.Fatalf("owner put: %d %s", r.status, r.body)
	}
	// The owner signed in on the site writes too.
	ownerCookie := s.a.session(t, s.olive, s.shopID, s.dom)
	if r := s.as(t, ownerCookie, "PUT", "/v1/sites/shop/data/menu", map[string]any{"soup": 5, "tea": 2}); r.status != 200 {
		t.Fatalf("owner browser put: %d %s", r.status, r.body)
	}
	got := s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/menu", nil, nil).json(t)
	if d, _ := got["data"].(map[string]any); d["tea"] != float64(2) {
		t.Fatalf("read back: %v", got)
	}
	// One document, and every change is in its history (restorable).
	var rows int
	_ = s.a.database.QueryRow(`SELECT count(*) FROM collection_items WHERE site_id = $1 AND collection = 'menu'`, s.shopID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("page info rows: %d", rows)
	}
	hist := historyOf(t, s.owner(t, "GET", "/v1/sites/shop/collections/menu/history", nil))
	if len(hist) != 1 {
		t.Fatalf("history: %v", hist)
	}
	hid := strconv.FormatInt(int64(hist[0]["id"].(float64)), 10)
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/menu/history/"+hid+"/restore", nil); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	got = s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/menu", nil, nil).json(t)
	if d, _ := got["data"].(map[string]any); d["soup"] != float64(4) || d["tea"] != nil {
		t.Fatalf("after restore: %v", got)
	}
	// Privacy cannot be set on page info.
	wantCode(t, "privacy on page info", s.owner(t, "PUT", "/v1/sites/shop/collections/menu/privacy", map[string]bool{"private": true}), 409, "wrong_kind")
	// Limits: document size, and names per site.
	s.setLimits(t, func(c *config.SavedData) { c.ContentMaxKB = 1; c.ContentNamesMax = 2 })
	wantCode(t, "too large", s.owner(t, "PUT", "/v1/sites/shop/data/menu", map[string]any{"x": strings.Repeat("a", 2000)}), 413, "item_too_large")
	s.declare(t, "hours", map[string]any{"kind": "content"})
	wantCode(t, "third name", s.owner(t, "PUT", "/v1/sites/shop/data/prices/kind", map[string]any{"kind": "content"}), 409, "too_many_names")
	// A list with several entries cannot become one document.
	s.declare(t, "notes", map[string]any{"kind": "entries", "visibility": "public"})
	for i := 0; i < 2; i++ {
		if r := s.visitor(t, "POST", "/v1/sites/shop/data/notes", map[string]any{"i": i}); r.status != 201 {
			t.Fatalf("note: %d %s", r.status, r.body)
		}
	}
	s.setLimits(t, func(c *config.SavedData) {})
	wantCode(t, "list to page info", s.owner(t, "PUT", "/v1/sites/shop/data/notes/kind", map[string]any{"kind": "content"}), 409, "has_entries")
	wantCode(t, "bad kind", s.owner(t, "PUT", "/v1/sites/shop/data/x/kind", map[string]any{"kind": "board"}), 400, "invalid_kind")
	wantCode(t, "visitor declares", s.visitor(t, "PUT", "/v1/sites/shop/data/x/kind", map[string]any{"kind": "content"}), 401, "")
}

func TestSubmissionsRules(t *testing.T) {
	s := newKindsSite(t, false)
	out := s.declare(t, "rsvps", map[string]any{"kind": "entries"})
	if out["visibility"] != "owner" || out["notify"] != "daily" || out["one_per_person"] != false {
		t.Fatalf("defaults: %v", out)
	}
	base := "/v1/sites/shop/data/rsvps"
	vicID := idOf(t, s.as(t, s.vicCooky, "POST", base, map[string]any{"name": "Vic", "guests": 1, "_submitted_by": "forged@x"}))
	wesID := idOf(t, s.as(t, s.wesCooky, "POST", base, map[string]any{"name": "Wes"}))
	wantCode(t, "key adds to private", s.ownerPage(t, "POST", base, map[string]any{"name": "Olive"}), 403, "private_visitor_only")

	// The owner reads all, with who sent each.
	all := itemsOf(t, s.owner(t, "GET", base, nil))
	if len(all) != 2 || all[0]["by"] != s.wes.email || all[1]["by"] != s.vic.email {
		t.Fatalf("owner read: %v", all)
	}
	// Nobody else reads the list, or its count.
	wantCode(t, "anonymous read", s.a.at(t, "GET", s.dom, base, nil, nil), 404, "not_found")
	wantCode(t, "visitor read", s.as(t, s.wesCooky, "GET", base, nil), 404, "not_found")
	wantCode(t, "visitor count", s.as(t, s.wesCooky, "GET", base+"?count=1", nil), 404, "not_found")
	if c := s.owner(t, "GET", base+"?count=1", nil).json(t); c["count"] != float64(2) {
		t.Fatalf("owner count: %v", c)
	}
	// Each visitor sees their own.
	mine := itemsOf(t, s.as(t, s.vicCooky, "GET", base+"?mine=1", nil))
	if len(mine) != 1 || strconv.FormatInt(int64(mine[0]["id"].(float64)), 10) != vicID {
		t.Fatalf("vic's own: %v", mine)
	}
	if d := mine[0]["data"].(map[string]any); d["_submitted_by"] != s.vic.email {
		t.Fatalf("stamp: %v", d)
	}
	wantCode(t, "mine, not signed in", s.a.at(t, "GET", s.dom, base+"?mine=1", nil, nil), 401, "visitor_auth_required")

	// Each changes only their own; the stamps never change.
	r := s.as(t, s.vicCooky, "PATCH", base+"/items/"+vicID, map[string]any{"guests": 3, "_submitted_by": "evil@x"})
	if r.status != 200 {
		t.Fatalf("own edit: %d %s", r.status, r.body)
	}
	var it struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(r.body, &it)
	if it.Data["guests"] != float64(3) || it.Data["_submitted_by"] != s.vic.email {
		t.Fatalf("edited: %v", it.Data)
	}
	wantCode(t, "edit another's", s.as(t, s.vicCooky, "PATCH", base+"/items/"+wesID, map[string]any{"name": "x"}), 404, "not_found")
	wantCode(t, "withdraw another's", s.as(t, s.vicCooky, "DELETE", base+"/items/"+wesID, nil), 404, "not_found")
	wantCode(t, "edit without CSRF", s.a.at(t, "PATCH", s.dom, base+"/items/"+vicID, map[string]any{"a": 1}, browser(s.dom, s.vicCooky, "X-SH-CSRF", "")), 403, "csrf_required")

	// Withdraw, and undo within the window.
	w := s.as(t, s.vicCooky, "DELETE", base+"/items/"+vicID, nil)
	if w.status != 200 || w.json(t)["undo_minutes"] != float64(10) {
		t.Fatalf("withdraw: %d %s", w.status, w.body)
	}
	if n := len(itemsOf(t, s.as(t, s.vicCooky, "GET", base+"?mine=1", nil))); n != 0 {
		t.Fatalf("withdrawn still listed: %d", n)
	}
	wantCode(t, "someone else undoes", s.as(t, s.wesCooky, "POST", base+"/items/"+vicID+"/undo", nil), 404, "not_found")
	if r := s.as(t, s.vicCooky, "POST", base+"/items/"+vicID+"/undo", nil); r.status != 200 {
		t.Fatalf("undo: %d %s", r.status, r.body)
	}
	if n := len(itemsOf(t, s.as(t, s.vicCooky, "GET", base+"?mine=1", nil))); n != 1 {
		t.Fatalf("after undo: %d", n)
	}
	// Past the window, only the owner brings it back.
	s.as(t, s.vicCooky, "DELETE", base+"/items/"+vicID, nil)
	if _, err := s.a.database.Exec(`UPDATE data_history SET created_at = now() - interval '11 minutes' WHERE item_id = $1 AND op = 'delete'`, vicID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "late undo", s.as(t, s.vicCooky, "POST", base+"/items/"+vicID+"/undo", nil), 409, "undo_expired")
	if r := s.owner(t, "POST", "/v1/sites/shop/collections/rsvps/items/"+vicID+"/restore", nil); r.status != 200 {
		t.Fatalf("owner restore: %d %s", r.status, r.body)
	}
	// The owner's delete cannot be undone by the visitor.
	if r := s.owner(t, "DELETE", base+"/items/"+wesID, nil); r.status != 204 {
		t.Fatalf("owner delete via data route: %d %s", r.status, r.body)
	}
	wantCode(t, "undo the owner's delete", s.as(t, s.wesCooky, "POST", base+"/items/"+wesID+"/undo", nil), 409, "undo_expired")

	// Made public: only when the owner confirms, since the entry becomes
	// readable by anyone; then anyone reads, without who sent it.
	cp := s.owner(t, "PUT", "/v1/sites/shop/data/rsvps/kind", map[string]any{"kind": "entries", "visibility": "public"})
	wantCode(t, "private to public, unconfirmed", cp, 409, "confirm_public")
	if !strings.Contains(string(cp.body), "1 private entry") {
		t.Fatalf("confirm_public says what becomes public: %s", cp.body)
	}
	wantCode(t, "private to page info, unconfirmed", s.owner(t, "PUT", "/v1/sites/shop/data/rsvps/kind", map[string]any{"kind": "content"}), 409, "confirm_public")
	if out := s.declare(t, "rsvps", map[string]any{"kind": "entries", "visibility": "public", "confirm_public": true}); out["notify"] != "daily" {
		t.Fatalf("a re-declaration keeps notify: %v", out)
	}
	pub := itemsOf(t, s.a.at(t, "GET", s.dom, base, nil, nil))
	if len(pub) != 1 || pub[0]["by"] != nil {
		t.Fatalf("public read: %v", pub)
	}
	if d := pub[0]["data"].(map[string]any); d["_submitted_by"] != nil {
		t.Fatalf("public read shows the sender: %v", d)
	}
	if c := s.a.at(t, "GET", s.dom, base+"?count=1", nil, nil).json(t); c["count"] != float64(1) {
		t.Fatalf("public count: %v", c)
	}
	// A first public declaration emails nobody by default.
	if out := s.declare(t, "guestbook", map[string]any{"kind": "entries", "visibility": "public"}); out["notify"] != "off" {
		t.Fatalf("public default notify: %v", out)
	}
}

func TestOnePerPersonAndEntryLimits(t *testing.T) {
	s := newKindsSite(t, false)
	s.declare(t, "votes", map[string]any{"kind": "entries", "visibility": "public", "one_per_person": true})
	base := "/v1/sites/shop/data/votes"
	first := idOf(t, s.as(t, s.vicCooky, "POST", base, map[string]any{"pick": "a"}))
	r := s.as(t, s.vicCooky, "POST", base, map[string]any{"pick": "b"})
	wantCode(t, "second vote", r, 409, "one_per_person")
	if strconv.FormatInt(int64(r.json(t)["id"].(float64)), 10) != first {
		t.Fatalf("one_per_person names the existing entry: %s", r.body)
	}
	if r := s.as(t, s.vicCooky, "PATCH", base+"/items/"+first, map[string]any{"pick": "b"}); r.status != 200 {
		t.Fatalf("change vote: %d %s", r.status, r.body)
	}
	s.as(t, s.vicCooky, "DELETE", base+"/items/"+first, nil)
	idOf(t, s.as(t, s.vicCooky, "POST", base, map[string]any{"pick": "c"}))
	wantCode(t, "undo into a second vote", s.as(t, s.vicCooky, "POST", base+"/items/"+first+"/undo", nil), 409, "one_per_person")
	if r := s.as(t, s.wesCooky, "POST", base, map[string]any{"pick": "a"}); r.status != 201 {
		t.Fatalf("another person votes: %d %s", r.status, r.body)
	}

	s.declare(t, "notes", map[string]any{"kind": "entries", "visibility": "public"})
	s.setLimits(t, func(c *config.SavedData) { c.EntryMaxKB = 1; c.EntriesMax = 2 })
	wantCode(t, "entry too large", s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/notes", map[string]any{"t": strings.Repeat("a", 1500)}), 413, "item_too_large")
	for i := 0; i < 2; i++ {
		if r := s.as(t, s.vicCooky, "POST", "/v1/sites/shop/collections/notes", map[string]any{"i": i}); r.status != 201 {
			t.Fatalf("note %d: %d %s", i, r.status, r.body)
		}
	}
	wantCode(t, "list full", s.as(t, s.wesCooky, "POST", "/v1/sites/shop/data/notes", map[string]any{"i": 3}), 409, "list_full")
}

func TestWhoMaySave(t *testing.T) {
	s := newKindsSite(t, true)
	s.declare(t, "rsvps", map[string]any{"kind": "entries", "visibility": "public"})
	base := "/v1/sites/shop/data/rsvps"
	wantCode(t, "bad pattern", s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "listed", "allow": []string{"not an address"}}), 400, "invalid_savers")
	// Only vic, by address.
	r := s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "listed", "allow": []string{strings.ToUpper(s.vic.email)}})
	if r.status != 200 {
		t.Fatalf("savers: %d %s", r.status, r.body)
	}
	if got := s.owner(t, "GET", "/v1/sites/shop/savers", nil).json(t); got["mode"] != "listed" || got["allow"].([]any)[0] != s.vic.email {
		t.Fatalf("savers read back: %v", got)
	}
	vicEntry := idOf(t, s.as(t, s.vicCooky, "POST", base, map[string]any{"n": 1}))
	wantCode(t, "not listed", s.as(t, s.wesCooky, "POST", base, map[string]any{"n": 2}), 403, "not_allowed_to_save")
	// Site-wide: legacy lists and page data too.
	wantCode(t, "not listed, legacy list", s.as(t, s.wesCooky, "POST", "/v1/sites/shop/collections/guestbook", map[string]any{"n": 2}), 403, "not_allowed_to_save")
	wantCode(t, "not listed, page data", s.as(t, s.wesCooky, "PATCH", "/v1/sites/shop/state", map[string]any{"ops": []any{map[string]any{"op": "inc", "path": "n"}}}), 403, "not_allowed_to_save")
	// The owner always may.
	if r := s.ownerPage(t, "POST", base, map[string]any{"n": 0}); r.status != 201 {
		t.Fatalf("owner: %d %s", r.status, r.body)
	}
	// A whole domain (every test person is @example.com).
	if r := s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "listed", "allow": []string{"example.com"}}); r.status != 200 || !strings.Contains(string(r.body), `"@example.com"`) {
		t.Fatalf("domain: %d %s", r.status, r.body)
	}
	if r := s.as(t, s.wesCooky, "POST", base, map[string]any{"n": 2}); r.status != 201 {
		t.Fatalf("domain allows wes: %d %s", r.status, r.body)
	}
	// Block the person behind one entry: back to anyone, vic blocked.
	if r := s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "anyone"}); r.status != 200 {
		t.Fatalf("anyone: %d %s", r.status, r.body)
	}
	b := s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"collection": "rsvps", "id": vicEntry})
	if b.status != 200 || b.json(t)["blocked"] != s.vic.email {
		t.Fatalf("block: %d %s", b.status, b.body)
	}
	wantCode(t, "blocked adds", s.as(t, s.vicCooky, "POST", base, map[string]any{"n": 3}), 403, "not_allowed_to_save")
	wantCode(t, "blocked edits", s.as(t, s.vicCooky, "PATCH", base+"/items/"+vicEntry, map[string]any{"n": 3}), 403, "not_allowed_to_save")
	// A blocked person can still withdraw what they sent.
	if r := s.as(t, s.vicCooky, "DELETE", base+"/items/"+vicEntry, nil); r.status != 200 {
		t.Fatalf("blocked withdraws: %d %s", r.status, r.body)
	}
	if r := s.as(t, s.wesCooky, "POST", base, map[string]any{"n": 4}); r.status != 201 {
		t.Fatalf("others still save: %d %s", r.status, r.body)
	}
	// A blocked domain.
	if r := s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"email": "@example.com"}); r.status != 200 {
		t.Fatalf("block domain: %d %s", r.status, r.body)
	}
	wantCode(t, "blocked domain", s.as(t, s.wesCooky, "POST", base, map[string]any{"n": 5}), 403, "not_allowed_to_save")
	s.setLimits(t, func(c *config.SavedData) { c.SaversMax = 2 })
	wantCode(t, "too many", s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"email": "x@y.org"}), 400, "too_many_savers")
	// The owner's list of names shows kinds and the setting.
	d := s.owner(t, "GET", "/v1/sites/shop/data", nil).json(t)
	if d["undeclared_names_take_saves"] != true || d["savers"].(map[string]any)["mode"] != "anyone" {
		t.Fatalf("list data: %v", d)
	}
	found := false
	for _, n := range d["names"].([]any) {
		m := n.(map[string]any)
		if m["name"] == "rsvps" {
			found = m["kind"] == "entries" && m["label"] == "Submissions"
		}
	}
	if !found {
		t.Fatalf("rsvps not listed with its kind: %v", d["names"])
	}
}

func TestSubmissionEmails(t *testing.T) {
	s := newKindsSite(t, false)
	m := &noticeMailer{}
	s.a.sites.mailer = m
	ctx := context.Background()
	s.declare(t, "orders", map[string]any{"kind": "entries"}) // private: daily
	s.declare(t, "wall", map[string]any{"kind": "entries", "visibility": "public"})
	base := "/v1/sites/shop/data/orders"
	for _, c := range []string{s.vicCooky, s.wesCooky} {
		idOf(t, s.as(t, c, "POST", base, map[string]any{"item": "tea"}))
	}
	idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/wall", map[string]any{"hi": 1}))
	ownerCookie := s.a.session(t, s.olive, s.shopID, s.dom)
	idOf(t, s.as(t, ownerCookie, "POST", base, map[string]any{"item": "test"})) // the owner's own: not counted
	mine := func() []string {
		m.mu.Lock()
		defer m.mu.Unlock()
		var out []string
		for _, n := range m.sent {
			if strings.HasPrefix(n, s.olive.email+"|") {
				out = append(out, n)
			}
		}
		return out
	}
	s.a.sites.sendSubmissionEmails(ctx)
	if n := len(mine()); n != 0 {
		t.Fatalf("daily digest before a day: %d", n)
	}
	if _, err := s.a.database.Exec(`UPDATE collection_settings SET declared_at = now() - interval '25 hours', updated_at = now() - interval '25 hours' WHERE site_id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	s.a.sites.sendSubmissionEmails(ctx)
	got := mine()
	if len(got) != 1 || !strings.Contains(got[0], "|2 new entries in orders on shop|") {
		t.Fatalf("digest: %q", got)
	}
	s.a.sites.sendSubmissionEmails(ctx)
	if len(mine()) != 1 {
		t.Fatalf("sent twice: %q", mine())
	}
	// Batched: "each" sends one email per interval for what arrived.
	s.declare(t, "orders", map[string]any{"kind": "entries", "notify": "each"})
	idOf(t, s.as(t, s.vicCooky, "POST", base, map[string]any{"item": "cake"}))
	s.a.sites.sendSubmissionEmails(ctx)
	if len(mine()) != 1 {
		t.Fatalf("each: sent inside the interval: %q", mine())
	}
	// Eleven minutes on: the interval has passed; only the cake is new.
	if _, err := s.a.database.Exec(`UPDATE collection_settings SET notify_sent_at = notify_sent_at - interval '11 minutes' WHERE site_id = $1 AND collection = 'orders'`, s.shopID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.database.Exec(`UPDATE collection_items SET created_at = created_at - interval '11 minutes' WHERE site_id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	s.a.sites.sendSubmissionEmails(ctx)
	got = mine()
	if len(got) != 2 || !strings.Contains(got[1], "|1 new entry in orders on shop|") {
		t.Fatalf("each: %q", got)
	}
	// The stop link: GET asks, POST stops.
	i := strings.Index(got[1], "/v1/data-notify/stop?t=")
	link := strings.Fields(got[1][i:])[0]
	u, _ := url.Parse(link)
	if r := s.a.at(t, "GET", pcSiteDomain, link, nil, nil); r.status != 200 || !strings.Contains(string(r.body), "Stop these emails") {
		t.Fatalf("stop page: %d", r.status)
	}
	form := url.Values{"t": {u.Query().Get("t")}}
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/data-notify/stop", form.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); r.status != 200 {
		t.Fatalf("stop: %d %s", r.status, r.body)
	}
	set, _ := db.GetDataSettings(ctx, s.a.database, s.shopID, "orders")
	if set.Notify != "off" {
		t.Fatalf("after stop: %q", set.Notify)
	}
	if r := s.a.at(t, "GET", pcSiteDomain, "/v1/data-notify/stop?t=forged.x", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("forged link: %d", r.status)
	}
}

// Owner decision 2026-09-27: a name nobody declared is Shared. On the
// default install a site made after the kinds takes saves to it as before, so
// old skills, AI create and uploaded pages (no X-Skill-Version) keep working;
// SAVED_DATA_DEFAULT_KIND=declare_first makes such a site strict, and sites
// from before the kinds stay Shared either way.
func TestSharedIsTheDefault(t *testing.T) {
	s := newKindsSite(t, true)
	if _, err := s.a.database.Exec(`UPDATE sites SET legacy_data = false WHERE id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	s.a.sites.SetSavedData(config.DefaultSavedData())
	if r := s.visitor(t, "POST", "/v1/sites/shop/data/guestbook", map[string]any{"msg": "hi"}); r.status != 201 {
		t.Fatalf("visitor, undeclared, default install: %d %s", r.status, r.body)
	}
	if r := s.visitor(t, "POST", "/v1/sites/shop/collections/guestbook", map[string]any{"msg": "again"}); r.status != 201 {
		t.Fatalf("old route: %d %s", r.status, r.body)
	}
	if items := itemsOf(t, s.a.at(t, "GET", s.dom, "/v1/sites/shop/collections/guestbook", nil, nil)); len(items) != 2 {
		t.Fatalf("anyone reads a Shared name: %v", items)
	}
	k := s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/guestbook/kind", nil, nil).json(t)
	if k["declared"] != false || k["kind"] != nil || k["label"] != "Shared" || k["accepts_saves"] != true || k["visibility"] != "public" {
		t.Fatalf("kind of a Shared name: %v", k)
	}
	d := s.owner(t, "GET", "/v1/sites/shop/data", nil).json(t)
	if d["undeclared_names_take_saves"] != true {
		t.Fatalf("list data: %v", d)
	}
	for _, n := range d["names"].([]any) {
		if m := n.(map[string]any); m["name"] == "guestbook" && m["label"] != "Shared" {
			t.Fatalf("label: %v", m)
		}
	}
	// A site deployed with no X-Skill-Version (an old skill, AI create, the
	// dashboard's upload) takes saves under any name, the owner's included.
	s.a.deploy(t, s.olive, "fresh")
	var legacy bool
	if err := s.a.database.QueryRow(`SELECT legacy_data FROM sites WHERE id = $1`, s.a.siteID(t, s.olive, "fresh")).Scan(&legacy); err != nil || legacy {
		t.Fatalf("new site legacy_data=%v err=%v", legacy, err)
	}
	if r := s.a.at(t, "POST", pcSiteDomain, "/v1/sites/fresh/collections/rsvps", map[string]any{"a": 1}, map[string]string{"X-API-Key": s.olive.key, "Origin": "https://" + pcContentHost}); r.status != 201 {
		t.Fatalf("fresh site, undeclared, default install: %d %s", r.status, r.body)
	}

	// declare_first: the site made after the kinds refuses; one from before does not.
	c := config.DefaultSavedData()
	c.DefaultKind = config.DefaultKindDeclareFirst
	s.a.sites.SetSavedData(c)
	t.Cleanup(func() { s.a.sites.SetSavedData(config.DefaultSavedData()) })
	wantCode(t, "declare_first install", s.visitor(t, "POST", "/v1/sites/shop/data/guestbook", map[string]any{"msg": "x"}), 409, "declare_first")
	if k := s.a.at(t, "GET", s.dom, "/v1/sites/shop/data/guestbook/kind", nil, nil).json(t); k["accepts_saves"] != false || k["label"] != "Not set" {
		t.Fatalf("kind under declare_first: %v", k)
	}
	if _, err := s.a.database.Exec(`UPDATE sites SET legacy_data = true WHERE id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	if r := s.visitor(t, "POST", "/v1/sites/shop/data/guestbook", map[string]any{"msg": "old site"}); r.status != 201 {
		t.Fatalf("site from before the kinds under declare_first: %d %s", r.status, r.body)
	}
}

// taggedPerson is another account for p's mailbox: p's address with a +tag.
func (s *kindsSite) taggedPerson(t *testing.T, p person) (person, string) {
	t.Helper()
	addr := strings.Replace(p.email, "@", "+two@", 1)
	key, _ := auth.GenerateAPIKey()
	u, err := db.CreateUser(context.Background(), s.a.database, addr, key, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, "t"+strconv.FormatInt(int64(len(addr)), 10)+strings.ReplaceAll(strings.Split(p.email, "@")[0], ".", "-"), u.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.a.database.Exec(`DELETE FROM users WHERE id = $1`, u.ID) })
	tp := person{email: addr, key: key}
	return tp, s.a.session(t, tp, s.shopID, s.dom)
}

// Review sd2: S1 (anonymous Submissions), S4 (+tag), S5 (undo keeps the
// rules), S6 (block uses the stamped address), S8 (the owner undoes), S10.
func TestSubmissionsReviewFixes(t *testing.T) {
	s := newKindsSite(t, true)
	s.declare(t, "votes", map[string]any{"kind": "entries", "visibility": "public", "one_per_person": true})
	base := "/v1/sites/shop/data/votes"

	// S1: a write that reaches the list as nobody (log mode, no CSRF header)
	// is refused for Submissions, and still taken by a Shared name.
	s.a.sites.writeAuthMode = "log"
	noCSRF := browser(s.dom, s.vicCooky, "X-SH-CSRF", "")
	wantCode(t, "anonymous to Submissions", s.a.at(t, "POST", s.dom, base, map[string]any{"pick": "a"}, noCSRF), 401, "visitor_auth_required")
	if r := s.a.at(t, "POST", s.dom, "/v1/sites/shop/data/wall", map[string]any{"hi": 1}, noCSRF); r.status != 201 {
		t.Fatalf("anonymous to a Shared name in log mode: %d %s", r.status, r.body)
	}
	s.a.sites.writeAuthMode = "on"

	// S4: one per person counts the address without its +tag.
	tagged, taggedCooky := s.taggedPerson(t, s.vic)
	vicVote := idOf(t, s.as(t, s.vicCooky, "POST", base, map[string]any{"pick": "a"}))
	r := s.as(t, taggedCooky, "POST", base, map[string]any{"pick": "b"})
	wantCode(t, "same mailbox, +tag", r, 409, "one_per_person")
	if _, has := r.json(t)["id"]; has {
		t.Fatalf("another account's entry id revealed: %s", r.body)
	}
	// Blocking by entry blocks the mailbox under any tag.
	b := s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"collection": "votes", "id": vicVote})
	if b.status != 200 || b.json(t)["blocked"] != s.vic.email {
		t.Fatalf("block: %d %s", b.status, b.body)
	}
	wantCode(t, "blocked, +tag", s.as(t, taggedCooky, "POST", "/v1/sites/shop/data/wall", map[string]any{"n": 1}), 403, "not_allowed_to_save")
	_ = tagged
	if r := s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "anyone"}); r.status != 200 {
		t.Fatalf("unblock: %d %s", r.status, r.body)
	}

	// S5: undo keeps the list's cap.
	s.declare(t, "slots", map[string]any{"kind": "entries", "visibility": "public"})
	s.setLimits(t, func(c *config.SavedData) { c.EntriesMax = 1 })
	slot := idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/slots", map[string]any{"at": 9}))
	s.as(t, s.vicCooky, "DELETE", "/v1/sites/shop/data/slots/items/"+slot, nil)
	idOf(t, s.as(t, s.wesCooky, "POST", "/v1/sites/shop/data/slots", map[string]any{"at": 9}))
	wantCode(t, "undo into a full list", s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/slots/items/"+slot+"/undo", nil), 409, "list_full")
	s.setLimits(t, func(*config.SavedData) {})

	// S8: the owner, signed in on the site, undoes their own withdrawal even
	// when only listed people may save.
	ownerCookie := s.a.session(t, s.olive, s.shopID, s.dom)
	own := idOf(t, s.as(t, ownerCookie, "POST", "/v1/sites/shop/data/wall", map[string]any{"from": "owner"}))
	if r := s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "listed", "allow": []string{"someone@else.org"}}); r.status != 200 {
		t.Fatalf("listed: %d %s", r.status, r.body)
	}
	if r := s.as(t, ownerCookie, "DELETE", "/v1/sites/shop/data/wall/items/"+own, nil); r.status != 204 && r.status != 200 {
		t.Fatalf("owner withdraws: %d %s", r.status, r.body)
	}
	if r := s.as(t, ownerCookie, "POST", "/v1/sites/shop/data/wall/items/"+own+"/undo", nil); r.status != 200 {
		t.Fatalf("owner undoes on a listed site: %d %s", r.status, r.body)
	}
	s.owner(t, "PUT", "/v1/sites/shop/savers", map[string]any{"mode": "anyone"})

	// S6: a public list's _submitted_by is what the page sent; with no
	// stamped address there is nobody to block.
	spoof := idOf(t, s.as(t, s.wesCooky, "POST", "/v1/sites/shop/data/wall", map[string]any{"_submitted_by": "victim@example.org"}))
	if _, err := s.a.database.Exec(`UPDATE collection_items SET submitted_email = NULL WHERE id = $1`, spoof); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "block a spoofed sender", s.owner(t, "POST", "/v1/sites/shop/savers/block", map[string]any{"collection": "wall", "id": spoof}), 409, "no_author")

	// S10: Submissions names per site.
	s.setLimits(t, func(c *config.SavedData) { c.EntriesNamesMax = 2 })
	wantCode(t, "third Submissions name", s.owner(t, "PUT", "/v1/sites/shop/data/more/kind", map[string]any{"kind": "entries", "visibility": "public"}), 409, "too_many_names")
	s.declare(t, "votes", map[string]any{"kind": "entries", "one_per_person": false}) // re-declaring one it has is fine
}

// S9: an email is claimed before it is sent, so it goes out once however
// many servers run the tick, and a failed send is not retried every tick.
func TestSubmissionEmailClaimedOnce(t *testing.T) {
	s := newKindsSite(t, true)
	s.declare(t, "orders", map[string]any{"kind": "entries", "notify": "each"})
	idOf(t, s.as(t, s.vicCooky, "POST", "/v1/sites/shop/data/orders", map[string]any{"item": "tea"}))
	if _, err := s.a.database.Exec(`UPDATE collection_settings SET declared_at = now() - interval '1 hour', updated_at = now() - interval '1 hour' WHERE site_id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.database.Exec(`UPDATE collection_items SET created_at = now() - interval '30 minutes' WHERE site_id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	due, err := db.DueNotifications(ctx, s.a.database, 10*time.Minute, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	var mine []db.NotifyDue
	for _, d := range due {
		if d.SiteID == s.shopID {
			mine = append(mine, d)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("due: %v", mine)
	}
	first, err1 := db.ClaimNotification(ctx, s.a.database, mine[0])
	second, err2 := db.ClaimNotification(ctx, s.a.database, mine[0])
	if err1 != nil || err2 != nil || !first || second {
		t.Fatalf("claims: %v %v (%v %v)", first, second, err1, err2)
	}
	// A failed send is not retried on the next tick.
	if _, err := s.a.database.Exec(`UPDATE collection_settings SET notify_sent_at = NULL WHERE site_id = $1`, s.shopID); err != nil {
		t.Fatal(err)
	}
	s.a.sites.mailer = failingNoticeMailer{}
	s.a.sites.sendSubmissionEmails(ctx)
	m := &noticeMailer{}
	s.a.sites.mailer = m
	s.a.sites.sendSubmissionEmails(ctx)
	for _, n := range m.sent {
		if strings.HasPrefix(n, s.olive.email+"|") {
			t.Fatalf("re-sent after a failure: %q", n)
		}
	}
}

type failingNoticeMailer struct{}

func (failingNoticeMailer) SendSignInCode(string, string, string) error { return nil }
func (failingNoticeMailer) SendNotice(string, string, string) error {
	return errors.New("smtp down")
}
