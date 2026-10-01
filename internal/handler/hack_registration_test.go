package handler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHackRegistrationApprovalAndTracks(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "regorg")
	participant := a.newPerson(t, "regpart")
	rejected := a.newPerson(t, "regreject")
	stranger := a.newPerson(t, "regstranger")
	secondOrg := a.newPerson(t, "regother")
	slug := a.makeEvent(t, org)
	other := a.makeEvent(t, secondOrg)
	markReady(t, a.certDir, slug)
	base := "/v1/hack/events/" + slug
	settings := map[string]any{"approval_required": true, "questions": []map[string]any{{"id": "why", "prompt": "Why join?", "required": true}}}
	wantTS(t, "settings", a.api(t, "PUT", base+"/registration", settings, org.key), 200, "")
	wantTS(t, "settings cross event", a.api(t, "PUT", "/v1/hack/events/"+other+"/registration", settings, org.key), 404, "event_not_found")
	wantTS(t, "tracks", a.api(t, "PUT", base+"/tracks", map[string]any{"tracks": []map[string]string{{"slug": "climate", "name": "Climate", "challenge": "Build a climate tool", "prize": "A trophy"}}}, org.key), 200, "")
	wantTS(t, "tracks denied", a.api(t, "PUT", base+"/tracks", map[string]any{"tracks": []any{}}, stranger.key), 404, "event_not_found")
	ev := a.api(t, "GET", base, nil, org.key).json(t)
	code := ev["organiser"].(map[string]any)["join_code"].(string)
	preview := a.api(t, "GET", "/v1/hack/join/"+code, nil, "").json(t)
	if !preview["approval_required"].(bool) || len(preview["signup_questions"].([]any)) != 1 {
		t.Fatal("signup settings absent from join preview")
	}
	join := func(p person, answer string) resp {
		return a.api(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": "Applicant", "answers": map[string]string{"why": answer}}, p.key)
	}
	wantTS(t, "required answer", join(participant, ""), 400, "answer_required")
	wantTS(t, "pending", join(participant, "To build"), 200, "")
	wantTS(t, "second pending", join(rejected, "Another reason"), 200, "")
	view := a.api(t, "GET", base, nil, participant.key).json(t)
	if view["me"].(map[string]any)["approval_status"] != "pending" {
		t.Fatalf("status: %v", view["me"])
	}
	wantTS(t, "pending team denied", a.api(t, "POST", base+"/teams", map[string]string{"name": "Early"}, participant.key), 403, "approval_pending")
	wantTS(t, "pending key denied", a.api(t, "POST", base+"/key", nil, participant.key), 403, "approval_pending")
	wantTS(t, "pending cannot choose track", a.api(t, "PUT", base+"/team/track", map[string]string{"track": "climate"}, participant.key), 403, "approval_pending")
	wantTS(t, "organiser cannot move pending", a.api(t, "POST", base+"/teams/no-team/members", map[string]string{"user_id": a.uid(t, participant)}, org.key), 404, "")
	applications := a.api(t, "GET", base+"/applications", nil, org.key)
	var apps []map[string]any
	if err := json.Unmarshal(applications.body, &apps); err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || !strings.Contains(string(applications.body), "To build") {
		t.Fatal("applications/answers missing")
	}
	wantTS(t, "applications denied", a.api(t, "GET", base+"/applications", nil, participant.key), 403, "approval_pending")
	decision := func(p person, status string) resp {
		return a.api(t, "POST", base+"/applications/"+a.uid(t, p)+"/decision", map[string]string{"decision": status}, org.key)
	}
	wantTS(t, "approve", decision(participant, "approved"), 200, "")
	wantTS(t, "reject", decision(rejected, "rejected"), 200, "")
	wantTS(t, "decision once", decision(rejected, "approved"), 409, "application_not_pending")
	wantTS(t, "rejected team denied", a.api(t, "POST", base+"/teams", map[string]string{"name": "No"}, rejected.key), 403, "approval_rejected")
	team, _ := a.startTeam(t, slug, "Approved team", participant)
	wantTS(t, "choose track", a.api(t, "PUT", base+"/team/track", map[string]string{"track": "climate"}, participant.key), 200, "")
	teamView := a.api(t, "GET", base, nil, participant.key).json(t)["me"].(map[string]any)["team"].(map[string]any)
	if teamView["track"].(map[string]any)["slug"] != "climate" {
		t.Fatal("own team track missing")
	}
	key := a.teamKey(t, slug, participant)
	wantTS(t, "publish", a.deployTeam(t, team, key, "approved"), 201, "")
	wantTS(t, "cross event choice denied", a.api(t, "PUT", "/v1/hack/events/"+other+"/team/track", map[string]string{"track": "climate"}, participant.key), 404, "event_not_found")
	wantTS(t, "archive", a.api(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	wantTS(t, "archived registration refused", a.api(t, "PUT", base+"/registration", settings, org.key), 409, "event_closed")
	wantTS(t, "archived tracks refused", a.api(t, "PUT", base+"/tracks", map[string]any{"tracks": []any{}}, org.key), 409, "event_closed")
	wantTS(t, "archived decision refused", decision(rejected, "approved"), 409, "event_closed")
}
