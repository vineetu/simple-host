package handler

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

func TestHackOnDemandTLSNames(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "tls-org")
	participant := a.newPerson(t, "tls-team")
	slug := a.makeEvent(t, org)
	a.join(t, slug, participant, org)
	team, _ := a.startTeam(t, slug, "TLS team", participant)
	ask := func(name string, want int) {
		t.Helper()
		r := a.api(t, "GET", "/internal/tls-ask?domain="+url.QueryEscape(name), nil, "")
		if r.status != want {
			t.Fatalf("TLS %s: got %d, want %d", name, r.status, want)
		}
	}
	ask(tsDomain, http.StatusOK)
	ask("www."+tsDomain, http.StatusOK)
	ask(slug+"."+tsDomain, http.StatusOK)
	ask("unknown-event."+tsDomain, http.StatusForbidden)
	ask("extra."+team+"."+slug+"."+tsDomain, http.StatusForbidden)
	ask("stranger."+slug+"."+tsDomain, http.StatusForbidden)
	ask(team+"."+slug+"."+tsDomain, http.StatusForbidden)
	markReady(t, a.certDir, slug)
	wantTS(t, "publish", a.deployTeam(t, team, a.teamKey(t, slug, participant), "Published"), http.StatusCreated, "")
	ask(team+"."+slug+"."+tsDomain, http.StatusOK)
	wantTS(t, "archive", a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]string{"stage": "archived"}, org.key), http.StatusOK, "")
	ask(team+"."+slug+"."+tsDomain, http.StatusOK)
	var account string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, slug).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err := a.sites.TrashTeamSite(context.Background(), account, team); err != nil {
		t.Fatal(err)
	}
	ask(team+"."+slug+"."+tsDomain, http.StatusForbidden)
	ask(slug+"."+tsDomain, http.StatusOK)
	// The ordinary small-box product retains its existing platform allowlist.
	SetHackMode(false)
	ask("ordinary-platform-name."+tsDomain, http.StatusOK)
}
