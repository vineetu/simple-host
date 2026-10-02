package handler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

// Covers the two Hack storage identities and the visitor's event origin.
func TestHackStorageScopedReview(t *testing.T) {
	a := newTeamSiteApp(t)
	organiser := a.newPerson(t, "storage-review-org")
	participant := a.newPerson(t, "storage-review-member")
	other := a.newPerson(t, "storage-review-other")
	slug := a.makeEvent(t, organiser)
	a.join(t, slug, participant, organiser)
	team, _ := a.startTeam(t, slug, "Review", participant)
	markReady(t, a.certDir, slug)
	key := a.teamKey(t, slug, participant)
	if r := a.deployTeam(t, team, key, "storage review"); r.status != 201 {
		t.Fatalf("team deploy: %d %s", r.status, r.body)
	}
	resource := map[string]string{"kind": "kv", "read": "anyone", "write": "owner"}
	teamPath := "/v1/sites/" + team + "/storage/resources/review"
	wantTS(t, "team create resource", a.api(t, "PUT", teamPath, resource, key), 201, "")
	// A second event may use the same team slug; the host and holding account
	// must still identify distinct storage owners.
	secondEvent := a.makeEvent(t, organiser)
	a.join(t, secondEvent, other, organiser)
	otherTeam, _ := a.startTeam(t, secondEvent, "Review", other)
	if otherTeam != team {
		t.Fatalf("test fixture did not reuse team slug: %q vs %q", otherTeam, team)
	}
	markReady(t, a.certDir, secondEvent)
	otherKey := a.teamKey(t, secondEvent, other)
	if r := a.deployTeam(t, otherTeam, otherKey, "other event"); r.status != 201 {
		t.Fatalf("second team deploy: %d %s", r.status, r.body)
	}
	wantTS(t, "same slug on first event host", a.on(t, "GET", team+"."+slug+"."+tsDomain, "/v1/sites/"+team+"/storage/resources", nil, otherKey), 403, "forbidden")
	wantTS(t, "personal cannot configure team", a.api(t, "PUT", teamPath, resource, participant.key), 404, "site_not_found")
	wantTS(t, "organiser cannot configure team", a.api(t, "PUT", teamPath, resource, organiser.key), 404, "site_not_found")
	wantTS(t, "other team member cannot configure team", a.api(t, "PUT", teamPath, resource, other.key), 404, "site_not_found")
	if r := a.api(t, "GET", "/v1/sites/"+team+"/storage/usage", nil, key); r.status != 200 {
		t.Fatalf("team usage: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/files?create=1", deployBody("event storage"), organiser.key); r.status != 201 {
		t.Fatalf("event website deploy: %d %s", r.status, r.body)
	}
	eventPath := "/v1/hack/events/" + slug + "/website/storage/resources/review"
	wantTS(t, "event resource", a.api(t, "PUT", eventPath, resource, organiser.key), 201, "")
	wantTS(t, "participant cannot configure event", a.api(t, "PUT", eventPath, resource, participant.key), 404, "event_not_found")
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/storage/kv/review/keys/hello", map[string]any{"value": "from event"}, organiser.key); r.status != 200 {
		t.Fatalf("event KV write: %d %s", r.status, r.body)
	}
	eventHost := slug + "." + tsDomain
	if r := a.on(t, "GET", eventHost, "/v1/sites/"+slug+"/storage/kv/review/keys/hello", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "from event") {
		t.Fatalf("event browser storage route: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", eventHost, "/v1/sites/"+slug+"/data/secret", nil, ""); r.status != 404 {
		t.Fatalf("unrelated event API: %d %s", r.status, r.body)
	}
	if r := a.on(t, http.MethodGet, eventHost, "/v1/sites/"+team+"/storage/resources", nil, ""); r.status != 404 {
		t.Fatalf("event host reached team storage: %d %s", r.status, r.body)
	}
	// A signed-in visitor's session is bound to this event site and host.
	signed := map[string]string{"kind": "kv", "read": "signed-in", "write": "signed-in"}
	wantTS(t, "event signed-in resource", a.api(t, "PUT", "/v1/hack/events/"+slug+"/website/storage/resources/signed", signed, organiser.key), 201, "")
	var eventSiteID string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN events e ON e.account_id=s.user_id AND e.id::text=s.name WHERE e.slug=$1`, slug).Scan(&eventSiteID); err != nil {
		t.Fatal(err)
	}
	var sessionID [32]byte
	if _, err := rand.Read(sessionID[:]); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertVisitorSession(context.Background(), a.database, sessionID[:], a.uid(t, other), eventSiteID, eventHost, time.Now().Add(time.Hour), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM visitor_sessions WHERE id=$1`, sessionID[:]) })
	request := func(host string, withCookie bool) resp {
		t.Helper()
		req, err := http.NewRequest("PUT", a.srv.URL+"/v1/sites/"+slug+"/storage/kv/signed/keys/hello", strings.NewReader(`{"value":"visitor"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = host
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("Origin", "https://"+host)
		req.Header.Set("X-SH-CSRF", "1")
		req.Header.Set("Content-Type", "application/json")
		if withCookie {
			req.AddCookie(&http.Cookie{Name: "__Host-sh_vsess", Value: hex.EncodeToString(sessionID[:])})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return resp{res.StatusCode, res.Header, body}
	}
	wantTS(t, "visitor without session", request(eventHost, false), 401, "sign_in_required")
	wantTS(t, "visitor with event session", request(eventHost, true), 200, "")
	wantTS(t, "event session on other host", request(team+"."+slug+"."+tsDomain, true), 404, "")

	// The unified personal MCP grant uses the selected team's key per call.
	clientID := a.registerClient(t, testRedirect)
	access := a.connect(t, participant, clientID, testRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (string, bool) {
		text, _, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
		return text, failed
	}
	if msg, failed := call("storage_get_usage", map[string]any{"site": team}); !failed || !strings.Contains(msg, "hack_select_team") {
		t.Fatalf("MCP before selection: %v %s", failed, msg)
	}
	var teamID string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id WHERE e.slug=$1 AND t.slug=$2`, slug, team).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if msg, failed := call("hack_select_team", map[string]any{"team_id": teamID}); failed {
		t.Fatalf("MCP team selection: %s", msg)
	}
	if msg, failed := call("storage_get_usage", map[string]any{"site": team}); failed || !strings.Contains(msg, "used_bytes") {
		t.Fatalf("MCP team usage: %v %s", failed, msg)
	}
	if msg, failed := call("storage_get_usage", map[string]any{"site": "wrong"}); !failed || !strings.Contains(msg, "team_site_only") {
		t.Fatalf("MCP wrong site: %v %s", failed, msg)
	}
	wantTS(t, "team file resource", a.api(t, "PUT", "/v1/sites/"+team+"/storage/resources/privatefiles", map[string]string{"kind": "files"}, key), 201, "")
	wantTS(t, "team file", a.api(t, "PUT", "/v1/sites/"+team+"/storage/files/privatefiles/objects/note.txt", "private note", key), 200, "")
	linkResponse := a.api(t, "POST", "/v1/sites/"+team+"/storage/files/privatefiles/download-link", map[string]string{"path": "note.txt"}, key)
	if linkResponse.status != 200 {
		t.Fatalf("file link mint status: %d", linkResponse.status)
	}
	linkURL, _ := linkResponse.json(t)["url"].(string)
	parsedLink, err := url.Parse(linkURL)
	if err != nil || parsedLink.Path != "/v1/storage-download" {
		t.Fatal("file link has invalid URL")
	}
	linkPath := parsedLink.RequestURI()
	if r := a.api(t, "GET", linkPath, nil, ""); r.status != 200 || !strings.Contains(string(r.body), "private note") {
		t.Fatalf("file link before revocation: status %d", r.status)
	}
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='pending' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.uid(t, participant)); err != nil {
		t.Fatal(err)
	}
	if msg, failed := call("storage_get_usage", map[string]any{"site": team}); !failed || !strings.Contains(msg, "hack_select_team") {
		t.Fatalf("MCP after membership revocation: %v %s", failed, msg)
	}
	wantTS(t, "file link after revocation", a.api(t, "GET", linkPath, nil, ""), 404, "file_link_invalid")
}
