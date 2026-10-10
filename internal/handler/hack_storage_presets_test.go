package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// hackSession signs uid in on host (bound to siteID) and returns a request
// function that sends the page's own headers from that host: Origin, the
// CSRF header and the session cookie ("" uid: signed out).
func hackSession(t *testing.T, a *teamSiteApp, uid, siteID, host string) func(method, path string, body any, extra ...string) resp {
	t.Helper()
	var cookie string
	if uid != "" {
		var id [32]byte
		if _, err := rand.Read(id[:]); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertVisitorSession(context.Background(), a.database, id[:], uid, siteID, host, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM visitor_sessions WHERE id=$1`, id[:]) })
		cookie = hex.EncodeToString(id[:])
	}
	return func(method, path string, body any, extra ...string) resp {
		t.Helper()
		var rd io.Reader
		if s, ok := body.(string); ok {
			rd = strings.NewReader(s)
		} else if body != nil {
			rd = jsonBody(body)
		}
		req, err := http.NewRequest(method, a.srv.URL+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Origin", "https://"+host)
		req.Header.Set("X-SH-CSRF", "1")
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(extra); i += 2 {
			req.Header.Set(extra[i], extra[i+1])
		}
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: "__Host-sh_vsess", Value: cookie})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return resp{res.StatusCode, res.Header, b}
	}
}

func hackSiteID(t *testing.T, a *teamSiteApp, slug, name string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN events e ON e.account_id=s.user_id WHERE e.slug=$1 AND s.name=$2 AND s.deleted_at IS NULL`, slug, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Storage access presets on a Simple Hack team site (owner decision
// 2026-10-10): the team's key and connector are the owner, a member signed
// in on the team site is the owner for saved data while the team may still
// change its site, and Hack's own gates still apply on top.
func TestHackStoragePresetsTeamSite(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member, member2 := a.newPerson(t, "hp-org"), a.newPerson(t, "hp-member"), a.newPerson(t, "hp-member2")
	alice, bob, rival := a.newPerson(t, "hp-alice"), a.newPerson(t, "hp-bob"), a.newPerson(t, "hp-rival")
	slug := a.makeEvent(t, org)
	for _, p := range []person{member, member2, rival} {
		a.join(t, slug, p, org)
	}
	team, code := a.startTeam(t, slug, "Presets", member)
	wantTS(t, "second member joins", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, member2.key), 200, "")
	rivalTeam, _ := a.startTeam(t, slug, "Rivals", rival)
	markReady(t, a.certDir, slug)
	key, rivalKey := a.teamKey(t, slug, member), a.teamKey(t, slug, rival)
	wantTS(t, "deploy team", a.deployTeam(t, team, key, "presets"), 201, "")
	wantTS(t, "deploy rival", a.deployTeam(t, rivalTeam, rivalKey, "rival"), 201, "")
	host := team + "." + slug + "." + tsDomain
	sid := hackSiteID(t, a, slug, team)
	base := "/v1/sites/" + team + "/storage"

	// The team key sets presets; the answer is the preset model.
	r := a.api(t, "PUT", base+"/resources/orders", map[string]any{"kind": "sqlite", "preset": "private", "tables": map[string]any{"orders": map[string]string{"preset": "records"}, "posts": map[string]string{"preset": "wall"}}}, key)
	wantTS(t, "team sets presets", r, 201, "")
	if got := r.json(t); got["preset"] != "private" || !strings.Contains(fmt.Sprint(got["tables"]), "records") {
		t.Fatalf("preset answer: %v", got)
	}
	wantTS(t, "R1 on Hack", a.api(t, "PUT", base+"/resources/open", map[string]string{"kind": "kv", "read": "anyone", "write": "anyone"}, key), 400, "")
	wantTS(t, "schema orders", a.api(t, "POST", base+"/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT, status TEXT DEFAULT 'new', created_at TEXT)"}, key), 200, "")
	wantTS(t, "schema posts", a.api(t, "POST", base+"/sqlite/orders/schema", map[string]string{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT, created_at TEXT)"}, key), 200, "")
	wantTS(t, "kv personal", a.api(t, "PUT", base+"/resources/prefs", map[string]string{"kind": "kv", "preset": "personal"}, key), 201, "")
	wantTS(t, "kv nobody", a.api(t, "PUT", base+"/resources/vault", map[string]string{"kind": "kv", "preset": "custom", "read": "nobody", "add": "nobody", "edit": "nobody", "delete": "nobody"}, key), 201, "")
	wantTS(t, "vault value", a.api(t, "PUT", base+"/kv/vault/keys/k", `{"value":"secret"}`, key), 200, "")
	if list := a.api(t, "GET", base+"/resources", nil, key); !strings.Contains(string(list.body), `"preset":"personal"`) {
		t.Fatalf("list: %s", list.body)
	}

	asAlice := hackSession(t, a, a.uid(t, alice), sid, host)
	asBob := hackSession(t, a, a.uid(t, bob), sid, host)
	asMember := hackSession(t, a, a.uid(t, member), sid, host)
	asMember2 := hackSession(t, a, a.uid(t, member2), sid, host)
	asRival := hackSession(t, a, a.uid(t, rival), sid, host)
	anon := hackSession(t, a, "", sid, host)

	// Records: each visitor sees only their own order.
	wantTS(t, "alice orders", asAlice("POST", base+"/sqlite/orders/tables/orders/rows", `{"item":"tea"}`), 200, "")
	wantTS(t, "bob orders", asBob("POST", base+"/sqlite/orders/tables/orders/rows", `{"item":"cake"}`), 200, "")
	wantTS(t, "anon cannot order", anon("POST", base+"/sqlite/orders/tables/orders/rows", `{"item":"x"}`), 401, "sign_in_required")
	if got := asBob("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status != 200 || strings.Contains(string(got.body), "tea") || !strings.Contains(string(got.body), "cake") || !strings.Contains(string(got.body), `"mine"`) {
		t.Fatalf("bob sees: %d %s", got.status, got.body)
	}
	wantTS(t, "bob cannot set status", asBob("PATCH", base+"/sqlite/orders/tables/orders/rows/2", `{"status":"done"}`), 403, "")

	// A member signed in on the team site is the owner for saved data.
	if got := asMember("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status != 200 || !strings.Contains(string(got.body), "tea") || !strings.Contains(string(got.body), "cake") {
		t.Fatalf("member on site: %d %s", got.status, got.body)
	}
	wantTS(t, "member sets status", asMember2("PATCH", base+"/sqlite/orders/tables/orders/rows/1", `{"status":"ready"}`), 200, "")
	if got := asAlice("GET", base+"/sqlite/orders/tables/orders/rows/1", nil); !strings.Contains(string(got.body), "ready") {
		t.Fatalf("alice sees status: %s", got.body)
	}
	if me := asMember("GET", "/v1/sites/"+team+"/me", nil).json(t); me["site_owner"] != true {
		t.Fatalf("member me: %v", me)
	}
	if me := asAlice("GET", "/v1/sites/"+team+"/me", nil).json(t); me["site_owner"] == true {
		t.Fatalf("alice me: %v", me)
	}
	// Storage data only: never settings, and never nobody.
	wantTS(t, "member on site cannot list resources", asMember("GET", base+"/resources", nil), 403, "")
	wantTS(t, "member on site cannot change presets", asMember("PUT", base+"/resources/prefs", map[string]string{"kind": "kv", "preset": "public"}), 403, "")
	wantTS(t, "member on site cannot read nobody", asMember("GET", base+"/kv/vault/keys/k", nil), 403, "")
	// A member of another team, signed in on this team's site, is a visitor.
	if got := asRival("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status != 200 || strings.Contains(string(got.body), "tea") {
		t.Fatalf("rival on site: %d %s", got.status, got.body)
	}
	if me := asRival("GET", "/v1/sites/"+team+"/me", nil).json(t); me["site_owner"] == true {
		t.Fatalf("rival me: %v", me)
	}
	// The other team's key never reaches this site's rows.
	wantTS(t, "rival key on rows", a.api(t, "PATCH", base+"/sqlite/orders/tables/orders/rows/1", `{"status":"x"}`, rivalKey), 403, "team_site_only")
	// The team key uses the row routes too.
	wantTS(t, "team key reads a row", a.api(t, "GET", base+"/sqlite/orders/tables/orders/rows/2", nil, key), 200, "")
	wantTS(t, "team key edits a row", a.api(t, "PATCH", base+"/sqlite/orders/tables/orders/rows/2", `{"status":"packed"}`, key), 200, "")

	// Wall: signed-in visitors post, authors delete their own.
	wantTS(t, "alice posts", asAlice("POST", base+"/sqlite/orders/tables/posts/rows", `{"body":"hello"}`), 200, "")
	wantTS(t, "bob cannot delete alice's post", asBob("DELETE", base+"/sqlite/orders/tables/posts/rows/1", nil), 404, "row_not_found")
	if got := anon("GET", base+"/sqlite/orders/tables/posts/rows", nil); got.status != 200 || !strings.Contains(string(got.body), "hello") {
		t.Fatalf("anon reads wall: %d %s", got.status, got.body)
	}
	// Personal KV: own reads only.
	wantTS(t, "alice saves prefs", asAlice("PUT", base+"/kv/prefs/keys/alice", `{"value":"dark"}`), 200, "")
	wantTS(t, "bob cannot read alice's prefs", asBob("GET", base+"/kv/prefs/keys/alice", nil), 404, "")
	if got := asAlice("GET", base+"/kv/prefs/keys", nil); !strings.Contains(string(got.body), `"mine":true`) {
		t.Fatalf("alice prefs list: %s", got.body)
	}

	// The page a member opens while signed in is never framed by another
	// origin; a visitor's page is unchanged.
	if page := a.on(t, "GET", host, "/", nil, ""); page.status != 200 {
		t.Fatalf("page: %d", page.status)
	}
	if page := asMember("GET", "/", nil); !strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Fatalf("member page headers: %v", page.header)
	}
	if page := asAlice("GET", "/", nil); strings.Contains(page.header.Get("Content-Security-Policy"), "frame-ancestors") {
		t.Fatalf("visitor page headers: %v", page.header)
	}
	// A framed page never has owner rights.
	wantTS(t, "framed member", asMember("GET", base+"/sqlite/orders/tables/orders/rows/1", nil, "X-SH-Framed", "1"), 404, "")

	// The deadline: the team's key stops, and a member on the site is an
	// ordinary visitor again. Visitors still add what the preset lets them.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() - interval '1 minute' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "team key frozen", a.api(t, "PATCH", base+"/sqlite/orders/tables/orders/rows/2", `{"status":"late"}`, key), 409, "submissions_closed")
	wantTS(t, "team key resource frozen", a.api(t, "PUT", base+"/resources/prefs", map[string]string{"kind": "kv", "preset": "public"}, key), 409, "submissions_closed")
	if got := asMember("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status != 200 || strings.Contains(string(got.body), "tea") {
		t.Fatalf("member after deadline: %d %s", got.status, got.body)
	}
	wantTS(t, "member after deadline cannot set status", asMember("PATCH", base+"/sqlite/orders/tables/orders/rows/1", `{"status":"late"}`), 403, "")
	if me := asMember("GET", "/v1/sites/"+team+"/me", nil).json(t); me["site_owner"] == true {
		t.Fatalf("member me after deadline: %v", me)
	}
	wantTS(t, "visitor still orders", asBob("POST", base+"/sqlite/orders/tables/orders/rows", `{"item":"late tea"}`), 200, "")
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = NULL WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}

	// A member who leaves the team stops being the owner.
	if _, err := a.database.Exec(`UPDATE event_members SET team_id = NULL WHERE user_id = $1`, a.uid(t, member2)); err != nil {
		t.Fatal(err)
	}
	if got := asMember2("GET", base+"/sqlite/orders/tables/orders/rows", nil); strings.Contains(string(got.body), "tea") {
		t.Fatalf("former member: %s", got.body)
	}

	// The organiser's take-down stops every page request, the owner's too.
	var teamID string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, slug, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if err := a.sites.SetTeamSiteTakenDown(context.Background(), accountID, teamID, team, true, "test"); err != nil {
		t.Fatal(err)
	}
	if got := asMember("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status == 200 {
		t.Fatalf("taken down, member read: %d %s", got.status, got.body)
	}
	if got := asAlice("POST", base+"/sqlite/orders/tables/orders/rows", `{"item":"x"}`); got.status == 200 {
		t.Fatalf("taken down, visitor add: %d %s", got.status, got.body)
	}
	if ok, err := db.HackSiteOwnerOnSite(context.Background(), a.database, sid, a.uid(t, member)); err != nil || ok {
		t.Fatalf("owner on a taken-down site: %v %v", ok, err)
	}
}

// Owner on an event's custom website is the organisers: their organiser
// routes (hack_event_storage_*) and an organiser signed in on the event
// website, while the event is neither ended nor taken down.
func TestHackStoragePresetsEventWebsite(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant, visitor := a.newPerson(t, "hpe-org"), a.newPerson(t, "hpe-member"), a.newPerson(t, "hpe-visitor")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	eventBase := "/v1/hack/events/" + slug + "/website"
	wantTS(t, "event site deploy", a.api(t, "PUT", eventBase+"/files?create=1", deployBody("event"), org.key), 201, "")
	st := eventBase + "/storage"
	wantTS(t, "inbox", a.api(t, "PUT", st+"/resources/questions", map[string]any{"kind": "sqlite", "preset": "inbox"}, org.key), 201, "")
	wantTS(t, "schema", a.api(t, "POST", st+"/sqlite/questions/schema", map[string]string{"sql": "CREATE TABLE questions (id INTEGER PRIMARY KEY, body TEXT, answered INTEGER DEFAULT 0, created_at TEXT)"}, org.key), 200, "")
	wantTS(t, "participant cannot set presets", a.api(t, "PUT", st+"/resources/questions", map[string]any{"kind": "sqlite", "preset": "public"}, participant.key), 404, "event_not_found")

	host := slug + "." + tsDomain
	sid := hackSiteID(t, a, slug, func() string {
		var id string
		_ = a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&id)
		return id
	}())
	path := "/v1/sites/" + slug + "/storage/sqlite/questions/tables/questions/rows"
	anon := hackSession(t, a, "", sid, host)
	asVisitor := hackSession(t, a, a.uid(t, visitor), sid, host)
	asOrg := hackSession(t, a, a.uid(t, org), sid, host)
	asParticipant := hackSession(t, a, a.uid(t, participant), sid, host)

	wantTS(t, "anyone sends", anon("POST", path, `{"body":"Is there food?"}`), 200, "")
	wantTS(t, "visitor sends", asVisitor("POST", path, `{"body":"Wifi?"}`), 200, "")
	wantTS(t, "visitor cannot read the inbox", asVisitor("GET", path, nil), 403, "")
	wantTS(t, "participant on site cannot read the inbox", asParticipant("GET", path, nil), 403, "")
	if got := asOrg("GET", path, nil); got.status != 200 || !strings.Contains(string(got.body), "food") || !strings.Contains(string(got.body), "Wifi") {
		t.Fatalf("organiser on site: %d %s", got.status, got.body)
	}
	wantTS(t, "organiser on site marks answered", asOrg("PATCH", path+"/1", `{"answered":1}`), 200, "")
	if me := asOrg("GET", "/v1/sites/"+slug+"/me", nil).json(t); me["site_owner"] != true {
		t.Fatalf("organiser me: %v", me)
	}
	if me := asParticipant("GET", "/v1/sites/"+slug+"/me", nil).json(t); me["site_owner"] == true {
		t.Fatalf("participant me: %v", me)
	}
	wantTS(t, "organiser on site cannot change presets", asOrg("PUT", "/v1/sites/"+slug+"/storage/resources/questions", map[string]any{"kind": "sqlite", "preset": "public"}), 403, "")
	// The organiser route reaches the row routes, PATCH included.
	wantTS(t, "organiser route reads a row", a.api(t, "GET", st+"/sqlite/questions/tables/questions/rows/2", nil, org.key), 200, "")
	wantTS(t, "organiser route edits a row", a.api(t, "PATCH", st+"/sqlite/questions/tables/questions/rows/2", `{"answered":1}`, org.key), 200, "")
	wantTS(t, "organiser route deletes a row", a.api(t, "DELETE", st+"/sqlite/questions/tables/questions/rows/2", nil, org.key), 200, "")
	wantTS(t, "participant cannot use the organiser route", a.api(t, "PATCH", st+"/sqlite/questions/tables/questions/rows/1", `{"answered":0}`, participant.key), 404, "event_not_found")

	// Ending the event: the organiser route stops changing, and an
	// organiser on the site is an ordinary visitor.
	wantTS(t, "archive", a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	wantTS(t, "organiser route after the end", a.api(t, "PATCH", st+"/sqlite/questions/tables/questions/rows/1", `{"answered":0}`, org.key), 409, "event_closed")
	if got := asOrg("GET", path, nil); got.status == 200 {
		t.Fatalf("organiser on site after the end: %d %s", got.status, got.body)
	}
}

// The connector: the selected team's storage_set_resource takes presets and
// get_page_recipe is offered; the event tools take presets too.
func TestHackStoragePresetsConnector(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member := a.newPerson(t, "hpc-org"), a.newPerson(t, "hpc-member")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Connector", member)
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, member)
	wantTS(t, "deploy", a.deployTeam(t, team, key, "c"), 201, "")
	wantTS(t, "event site", a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/files?create=1", deployBody("event"), org.key), 201, "")

	clientID := a.registerClient(t, testRedirect)
	call := func(access, name string, args map[string]any) (string, bool) {
		text, _, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
		return text, failed
	}
	access := a.connect(t, member, clientID, testRedirect)["access_token"].(string)
	var teamID string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, slug, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if msg, failed := call(access, "hack_select_team", map[string]any{"team_id": teamID}); failed {
		t.Fatalf("select: %s", msg)
	}
	if msg, failed := call(access, "storage_set_resource", map[string]any{"site": team, "name": "guestbook", "body": map[string]any{"kind": "sqlite", "preset": "wall"}}); failed || !strings.Contains(msg, `"preset": "wall"`) && !strings.Contains(msg, `"preset":"wall"`) {
		t.Fatalf("team preset: %v %s", failed, msg)
	}
	if msg, failed := call(access, "hack_get_page_recipe", map[string]any{"topic": "wall", "site": team}); failed || !strings.Contains(msg, "simple-hack") || strings.Contains(msg, "Simple Host account") {
		t.Fatalf("hack recipe: %v %.300s", failed, msg)
	}
	orgAccess := a.connect(t, org, clientID, testRedirect)["access_token"].(string)
	if msg, failed := call(orgAccess, "hack_event_storage_set_resource", map[string]any{"event": slug, "name": "questions", "body": map[string]any{"kind": "sqlite", "preset": "inbox"}}); failed || !strings.Contains(msg, "inbox") {
		t.Fatalf("event preset: %v %s", failed, msg)
	}
	tools := a.rpc(t, orgAccess, "tools/list", map[string]any{})
	raw := string(tools.body)
	if !strings.Contains(raw, "organisers") || strings.Contains(raw, "read own and write_mode add are not available") {
		t.Fatalf("event tool text not updated")
	}
}
