package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHackStorageTeamAndEventScopes(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member, outsider := a.newPerson(t, "storage-org"), a.newPerson(t, "storage-member"), a.newPerson(t, "storage-outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	team, _ := a.startTeam(t, slug, "Storage", member)
	key := a.teamKey(t, slug, member)
	markReady(t, a.certDir, slug)
	wantTS(t, "deploy team", a.deployTeam(t, team, key, "storage team"), 201, "")
	base := "/v1/sites/" + team + "/storage"
	resource := map[string]string{"kind": "kv", "read": "anyone", "write": "owner", "site_passcode": "inherit"}
	wantTS(t, "team create resource", a.api(t, "PUT", base+"/resources/prefs", resource, key), 201, "")
	wantTS(t, "team write key", a.api(t, "PUT", base+"/kv/prefs/keys/theme", `{"value":"blue"}`, key), 200, "")
	teamHost := team + "." + slug + "." + tsDomain
	if r := a.on(t, "GET", teamHost, base+"/kv/prefs/keys/theme", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "blue") {
		t.Fatalf("team visitor read: %d %s", r.status, r.body)
	}
	wantTS(t, "personal cannot own team storage", a.api(t, "GET", base+"/resources", nil, member.key), 404, "site_not_found")
	wantTS(t, "outsider cannot own team storage", a.api(t, "GET", base+"/resources", nil, outsider.key), 404, "site_not_found")
	wantTS(t, "team key cannot touch wrong site", a.api(t, "GET", "/v1/sites/wrong/storage/resources", nil, key), 403, "team_site_only")

	eventBase := "/v1/hack/events/" + slug + "/website"
	wantTS(t, "event site deploy", a.api(t, "PUT", eventBase+"/files?create=1", deployBody("event"), org.key), 201, "")
	eventStorage := eventBase + "/storage"
	wantTS(t, "organiser resource", a.api(t, "PUT", eventStorage+"/resources/prefs", resource, org.key), 201, "")
	wantTS(t, "organiser value", a.api(t, "PUT", eventStorage+"/kv/prefs/keys/theme", `{"value":"orange"}`, org.key), 200, "")
	eventHost := slug + "." + tsDomain
	if r := a.on(t, "GET", eventHost, "/v1/sites/"+slug+"/storage/kv/prefs/keys/theme", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "orange") {
		t.Fatalf("event visitor read: %d %s", r.status, r.body)
	}
	if id, ok := a.sites.PersonReturnSite(context.Background(), eventHost, "/"); !ok || id == "" {
		t.Fatal("event host did not bind sign-in to custom site's ID")
	}
	if r := hackBrowserRequest(t, a, "GET", eventHost, "/v1/sites/"+slug+"/me", nil); r.status != 200 {
		t.Fatalf("event visitor me: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", eventHost, "/v1/sites/"+team+"/storage/resources", nil, ""); r.status != 404 {
		t.Fatalf("event host reached team storage: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", eventHost, "/v1/sites/"+slug+"/data", nil, ""); r.status != 404 {
		t.Fatalf("event host opened unrelated API: %d %s", r.status, r.body)
	}
	wantTS(t, "outsider event resource", a.api(t, "GET", eventStorage+"/resources", nil, outsider.key), 404, "event_not_found")
	if r := a.api(t, http.MethodGet, eventStorage+"/usage", nil, org.key); r.status != 200 {
		t.Fatalf("event usage: %d %s", r.status, r.body)
	}
}

func hackBrowserRequest(t *testing.T, a *teamSiteApp, method, host, path string, body io.Reader) resp {
	t.Helper()
	req, err := http.NewRequest(method, a.srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Origin", "https://"+host)
	req.Header.Set("X-SH-CSRF", "1")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header, data}
}
