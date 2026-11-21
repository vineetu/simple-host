package servertest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accountrepo "github.com/vsriram/simple-host/host/access/repo"
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
		if _, err := accountrepo.InsertVisitorSession(context.Background(), a.database, id[:], uid, siteID, host, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
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

// Rule sets for common shapes of access, shared by the
// servertest storage tests. Owner is Hack's owner: the team's tools and a
// member signed in on the team site until the submission deadline, or the
// organisers until the event ends.
const (
	ruleOwner = `auth.role == \"owner\"`
	ruleOwn   = `visitor_id == auth.id || auth.role == \"owner\"`
	// privateRules: only the owner reads and writes (model.OwnerOnlyRules).
	privateRules = `{"version":1,"policies":[{"command":"read","using":"` + ruleOwner + `"},{"command":"add","check":"` + ruleOwner + `"},{"command":"edit","using":"` + ruleOwner + `"},{"command":"delete","using":"` + ruleOwner + `"}]}`
	// publicRules: anyone reads, the owner writes.
	publicRules = `{"version":1,"policies":[{"command":"read","using":"true"},{"command":"add","check":"` + ruleOwner + `"},{"command":"edit","using":"` + ruleOwner + `"},{"command":"delete","using":"` + ruleOwner + `"}]}`
	// inboxRules: anyone adds, the owner reads and changes.
	inboxRules = `{"version":1,"policies":[{"command":"read","using":"` + ruleOwner + `"},{"command":"add","check":"true"},{"command":"edit","using":"` + ruleOwner + `"},{"command":"delete","using":"` + ruleOwner + `"}]}`
	// wallRules: anyone reads, signed-in visitors add, authors delete their own.
	wallRules = `{"version":1,"policies":[{"command":"read","using":"true"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"` + ruleOwner + `"},{"command":"delete","using":"` + ruleOwn + `"}]}`
	// recordsRules: signed-in visitors add and read their own, the owner changes.
	recordsRules = `{"version":1,"policies":[{"command":"read","using":"` + ruleOwn + `"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"` + ruleOwner + `"},{"command":"delete","using":"` + ruleOwner + `"}]}`
	// personalRules: signed-in visitors keep their own.
	personalRules = `{"version":1,"policies":[{"command":"read","using":"` + ruleOwn + `"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"` + ruleOwn + `"},{"command":"delete","using":"` + ruleOwn + `"}]}`
	// boardRules: signed-in visitors read, add and edit; the owner deletes.
	boardRules = `{"version":1,"policies":[{"command":"read","using":"auth.signed_in"},{"command":"add","check":"auth.signed_in"},{"command":"edit","using":"auth.signed_in"},{"command":"delete","using":"` + ruleOwner + `"}]}`
	// nobodyRules: no visitor, the owner signed in on the site included.
	nobodyRules = `{"version":1,"policies":[]}`
)

func hackSiteID(t *testing.T, a *teamSiteApp, slug, name string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN events e ON e.account_id=s.user_id WHERE e.slug=$1 AND s.name=$2 AND s.deleted_at IS NULL`, slug, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Storage rules on a Simple Hack team site (owner decision 2026-10-10): the
// team's key and connector are the owner, a member signed in on the team
// site is the owner (auth.role == "owner") for saved data while the team may
// still change its site, and Hack's own gates still apply on top.
func TestHackStorageOwnerTeamSite(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member, member2 := a.newPerson(t, "hp-org"), a.newPerson(t, "hp-member"), a.newPerson(t, "hp-member2")
	alice, bob, rival := a.newPerson(t, "hp-alice"), a.newPerson(t, "hp-bob"), a.newPerson(t, "hp-rival")
	slug := a.makeEvent(t, org)
	for _, p := range []person{member, member2, rival} {
		a.join(t, slug, p, org)
	}
	team, code := a.startTeam(t, slug, "Owners", member)
	wantTS(t, "second member joins", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/join", map[string]string{"code": code}, member2.key), 200, "")
	rivalTeam, _ := a.startTeam(t, slug, "Rivals", rival)
	markReady(t, a.certDir, slug)
	key, rivalKey := member.key, rival.key
	wantTS(t, "deploy team", a.deployTeam(t, slug, team, key, "owners"), 201, "")
	wantTS(t, "deploy rival", a.deployTeam(t, slug, rivalTeam, rivalKey, "rival"), 201, "")
	host := team + "." + slug + "." + tsDomain
	sid := hackSiteID(t, a, slug, team)
	base := "/v1/sites/" + team + "/storage"
	own := "?owner=" + slug

	// A teammate's own key sets rules; the answer is the rules model.
	r := a.api(t, "PUT", base+"/resources/orders"+own, map[string]any{"kind": "sqlite", "rules": json.RawMessage(privateRules), "tables": map[string]any{"orders": map[string]any{"rules": json.RawMessage(recordsRules)}, "posts": map[string]any{"rules": json.RawMessage(wallRules)}}}, key)
	wantTS(t, "team sets rules", r, 201, "")
	if got := r.json(t); got["rules_status"] != "rules" || !strings.Contains(fmt.Sprint(got["tables"]), "visitor_id == auth.id") {
		t.Fatalf("rules answer: %v", got)
	}
	wantTS(t, "older fields on Hack", a.api(t, "PUT", base+"/resources/open"+own, map[string]string{"kind": "kv", "read": "anyone", "write": "anyone"}, key), 400, "invalid_resource")
	wantTS(t, "schema orders", a.api(t, "POST", base+"/sqlite/orders/schema"+own, map[string]string{"sql": "CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT, status TEXT DEFAULT 'new', created_at TEXT)"}, key), 200, "")
	wantTS(t, "schema posts", a.api(t, "POST", base+"/sqlite/orders/schema"+own, map[string]string{"sql": "CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT, created_at TEXT)"}, key), 200, "")
	wantTS(t, "kv personal", a.api(t, "PUT", base+"/resources/prefs"+own, map[string]any{"kind": "kv", "rules": json.RawMessage(personalRules)}, key), 201, "")
	wantTS(t, "kv nobody", a.api(t, "PUT", base+"/resources/vault"+own, map[string]any{"kind": "kv", "rules": json.RawMessage(nobodyRules)}, key), 201, "")
	wantTS(t, "vault value", a.api(t, "PUT", base+"/kv/vault/keys/k"+own, `{"value":"secret"}`, key), 200, "")
	if list := a.api(t, "GET", base+"/resources"+own, nil, key); !strings.Contains(string(list.body), `"name":"prefs"`) || !strings.Contains(string(list.body), `"rules_status":"rules"`) {
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
	wantTS(t, "member on site cannot change rules", asMember("PUT", base+"/resources/prefs", map[string]any{"kind": "kv", "rules": json.RawMessage(publicRules)}), 403, "")
	wantTS(t, "member on site cannot read nobody", asMember("GET", base+"/kv/vault/keys/k", nil), 403, "")
	// A member of another team, signed in on this team's site, is a visitor.
	if got := asRival("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status != 200 || strings.Contains(string(got.body), "tea") {
		t.Fatalf("rival on site: %d %s", got.status, got.body)
	}
	if me := asRival("GET", "/v1/sites/"+team+"/me", nil).json(t); me["site_owner"] == true {
		t.Fatalf("rival me: %v", me)
	}
	// Another team's member never reaches this site's rows.
	wantTS(t, "rival key on rows", a.api(t, "PATCH", base+"/sqlite/orders/tables/orders/rows/1"+own, `{"status":"x"}`, rivalKey), 404, "owner_not_found")
	// A teammate's key uses the row routes too.
	wantTS(t, "team key reads a row", a.api(t, "GET", base+"/sqlite/orders/tables/orders/rows/2"+own, nil, key), 200, "")
	wantTS(t, "team key edits a row", a.api(t, "PATCH", base+"/sqlite/orders/tables/orders/rows/2"+own, `{"status":"packed"}`, key), 200, "")

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

	// The deadline: a teammate's key stops, and a member on the site is an
	// ordinary visitor again. Visitors still add what the rules let them.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() - interval '1 minute' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "team key frozen", a.api(t, "PATCH", base+"/sqlite/orders/tables/orders/rows/2"+own, `{"status":"late"}`, key), 409, "submissions_closed")
	wantTS(t, "team key resource frozen", a.api(t, "PUT", base+"/resources/prefs"+own, map[string]any{"kind": "kv", "rules": json.RawMessage(publicRules)}, key), 409, "submissions_closed")
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
	wantTS(t, "member2 leaves", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/leave", nil, member2.key), 200, "")
	if got := asMember2("GET", base+"/sqlite/orders/tables/orders/rows", nil); strings.Contains(string(got.body), "tea") {
		t.Fatalf("former member: %s", got.body)
	}

	// The organiser's take-down stops every page request, the owner's too.
	wantTS(t, "take down team site", a.api(t, "POST", "/v1/hack/events/"+slug+"/teams/"+team+"/takedown", map[string]string{"reason": "test"}, org.key), 200, "")
	if got := asMember("GET", base+"/sqlite/orders/tables/orders/rows", nil); got.status == 200 {
		t.Fatalf("taken down, member read: %d %s", got.status, got.body)
	}
	if got := asAlice("POST", base+"/sqlite/orders/tables/orders/rows", `{"item":"x"}`); got.status == 200 {
		t.Fatalf("taken down, visitor add: %d %s", got.status, got.body)
	}
}

// Owner on an event's custom website is the organisers: their own keys with
// owner=<event> and an organiser signed in on the event website, while the event is neither ended nor taken down.
func TestHackStorageOwnerEventWebsite(t *testing.T) {
	a := newTeamSiteApp(t)
	org, participant, visitor := a.newPerson(t, "hpe-org"), a.newPerson(t, "hpe-member"), a.newPerson(t, "hpe-visitor")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	site := eventSite(t, a, slug, org)
	wantTS(t, "event site deploy", a.deployTeam(t, slug, site, org.key, "event"), 201, "")
	wantTS(t, "custom website", a.api(t, "PATCH", "/v1/hack/events/"+slug+"/website", map[string]string{"mode": "custom"}, org.key), 200, "")
	st := "/v1/sites/" + site + "/storage"
	own := "?owner=" + slug
	wantTS(t, "inbox", a.api(t, "PUT", st+"/resources/questions"+own, map[string]any{"kind": "sqlite", "rules": json.RawMessage(inboxRules)}, org.key), 201, "")
	wantTS(t, "schema", a.api(t, "POST", st+"/sqlite/questions/schema"+own, map[string]string{"sql": "CREATE TABLE questions (id INTEGER PRIMARY KEY, body TEXT, answered INTEGER DEFAULT 0, created_at TEXT)"}, org.key), 200, "")
	wantTS(t, "participant cannot set rules", a.api(t, "PUT", st+"/resources/questions"+own, map[string]any{"kind": "sqlite", "rules": json.RawMessage(publicRules)}, participant.key), 404, "owner_not_found")

	host := slug + "." + tsDomain
	sid := hackSiteID(t, a, slug, site)
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
	wantTS(t, "organiser on site cannot change rules", asOrg("PUT", "/v1/sites/"+slug+"/storage/resources/questions", map[string]any{"kind": "sqlite", "rules": json.RawMessage(publicRules)}), 403, "")
	// The organiser's own key reaches the row routes, PATCH included.
	wantTS(t, "organiser reads a row", a.api(t, "GET", st+"/sqlite/questions/tables/questions/rows/2"+own, nil, org.key), 200, "")
	wantTS(t, "organiser edits a row", a.api(t, "PATCH", st+"/sqlite/questions/tables/questions/rows/2"+own, `{"answered":1}`, org.key), 200, "")
	wantTS(t, "organiser deletes a row", a.api(t, "DELETE", st+"/sqlite/questions/tables/questions/rows/2"+own, nil, org.key), 200, "")
	wantTS(t, "participant cannot change the event website", a.api(t, "PATCH", st+"/sqlite/questions/tables/questions/rows/1"+own, `{"answered":0}`, participant.key), 404, "owner_not_found")

	// Ending the event: the organiser's key stops changing, and an
	// organiser on the site is an ordinary visitor.
	wantTS(t, "archive", a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	wantTS(t, "organiser after the end", a.api(t, "PATCH", st+"/sqlite/questions/tables/questions/rows/1"+own, `{"answered":0}`, org.key), 409, "event_closed")
	if got := asOrg("GET", path, nil); got.status == 200 {
		t.Fatalf("organiser on site after the end: %d %s", got.status, got.body)
	}
}

// eventSite is the event website's site name (the event's id), as the
// organiser's website screen reads it.
func eventSite(t *testing.T, a *teamSiteApp, slug string, org person) string {
	t.Helper()
	r := a.api(t, "GET", "/v1/hack/events/"+slug+"/website", nil, org.key)
	if r.status != 200 {
		t.Fatalf("event website: %d %s", r.status, r.body)
	}
	return r.json(t)["site"].(string)
}
