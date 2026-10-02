package handler

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHackEventWebsiteAndPublicRead(t *testing.T) {
	a := newTeamSiteApp(t)
	RegisterHackPublic(a.mux, a.database, a.srv.URL, a.sites.TeamSiteURL, a.sites.TeamSitesReady)
	org, outsider := a.newPerson(t, "website-org"), a.newPerson(t, "website-outsider")
	slug := a.makeEvent(t, org)
	host := slug + "." + tsDomain
	path := "/v1/hack/events/" + slug + "/website"
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "M2 test") {
		t.Fatalf("default event host: %d %s", r.status, r.body)
	}
	if r := a.api(t, "GET", path, nil, outsider.key); r.status != 404 {
		t.Fatalf("outsider website state: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PATCH", path, map[string]string{"mode": "custom"}, org.key); r.status != 409 {
		t.Fatalf("custom before upload: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PUT", path+"/files?create=1", deployBody("outsider"), outsider.key); r.status != 404 {
		t.Fatalf("outsider upload: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PUT", path+"/files?create=1", deployBody("custom event site"), org.key); r.status != 201 {
		t.Fatalf("website deploy: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "custom event site") {
		t.Fatalf("custom event host: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/v1/hack/events/"+slug+"/public", nil, ""); r.status != 404 {
		t.Fatalf("custom host API: %d", r.status)
	}
	if r := a.api(t, "GET", "/e/"+slug, nil, ""); r.status != 200 || !strings.Contains(string(r.body), "M2 test") {
		t.Fatalf("stable builtin: %d %s", r.status, r.body)
	}
	if r := a.api(t, "GET", "/v1/hack/events/"+slug+"/public", nil, ""); r.status != 200 || r.header.Get("Access-Control-Allow-Origin") != "*" || strings.Contains(string(r.body), "join_code") || strings.Contains(string(r.body), "contact_email") {
		t.Fatalf("public JSON: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PATCH", path, map[string]string{"mode": "builtin"}, org.key); r.status != 200 {
		t.Fatalf("switch builtin: %d %s", r.status, r.body)
	}
	if r := a.on(t, "GET", host, "/", nil, ""); r.status != 200 || !strings.Contains(string(r.body), "M2 test") || strings.Contains(string(r.body), "custom event site") {
		t.Fatalf("host switched back: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PATCH", path, map[string]string{"mode": "custom"}, org.key); r.status != 200 {
		t.Fatalf("restore custom: %d %s", r.status, r.body)
	}
}

func TestHackEventIconAndPreferences(t *testing.T) {
	a := newHackApp(t)
	org, other := a.newPerson(t, "icon-org"), a.newPerson(t, "icon-other")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	iconPath := "/v1/hack/events/" + slug + "/icon"
	if r := a.at(t, "GET", iconPath, nil, nil); r.status != 200 || !strings.Contains(string(r.body), "<svg") {
		t.Fatalf("default icon: %d %s", r.status, r.body)
	}
	put := func(p person, body []byte, typ string) int {
		req, _ := http.NewRequest("PUT", a.srv.URL+iconPath, bytes.NewReader(body))
		req.Header.Set("X-API-Key", p.key)
		req.Header.Set("Content-Type", typ)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if got := put(other, shotShown, "image/png"); got != 404 {
		t.Fatalf("outsider icon upload %d", got)
	}
	if got := put(org, []byte("<svg/>"), "image/png"); got != 400 {
		t.Fatalf("invalid icon %d", got)
	}
	if got := put(org, shotShown, "image/png"); got != 200 {
		t.Fatalf("valid icon %d", got)
	}
	if r := a.at(t, "GET", iconPath, nil, nil); r.status != 200 || r.header.Get("Content-Type") != "image/png" || !bytes.Equal(r.body, shotShown) {
		t.Fatalf("public icon %d %s", r.status, r.body)
	}
	pref := "/v1/hack/preferences"
	if r := a.at(t, "GET", pref, nil, a.key(org)); r.status != 200 || r.json(t)["theme"] != "system" {
		t.Fatalf("default prefs %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", pref, map[string]any{"theme": "sepia"}, a.key(org)); r.status != 400 {
		t.Fatalf("invalid theme %d", r.status)
	}
	if r := a.at(t, "PATCH", pref, map[string]any{"theme": "dark", "judge_walkthrough_done": true}, a.key(org)); r.status != 200 {
		t.Fatalf("save prefs %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pref, nil, a.key(org)); r.status != 200 || r.json(t)["theme"] != "dark" || r.json(t)["judge_walkthrough_done"] != true {
		t.Fatalf("saved prefs %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", pref, nil, a.key(other)); r.status != 200 || r.json(t)["theme"] != "system" {
		t.Fatalf("other prefs %d %s", r.status, r.body)
	}
}
