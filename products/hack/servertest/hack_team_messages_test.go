package servertest

import (
	"net/http"
	"testing"
)

// Only an organiser messages a team; the message reaches that team's
// members and no other team, and a participant or judge cannot send one.
func TestHackTeamMessagesWhoSeesWhat(t *testing.T) {
	a := newTeamSiteApp(t)
	org, alphaLead, betaLead, judge := a.newPerson(t, "tmsgorg"), a.newPerson(t, "tmsglead"), a.newPerson(t, "tmsgbeta"), a.newPerson(t, "tmsgjudge")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	a.join(t, slug, alphaLead, org)
	a.join(t, slug, betaLead, org)
	alphaTeam, _ := a.startTeam(t, slug, "Alpha", alphaLead)
	betaTeam, _ := a.startTeam(t, slug, "Beta", betaLead)
	code := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)["judge_code"].(string)
	wantTS(t, "judge joins", a.api(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true, "display_name": "J"}, judge.key), 200, "")
	var alphaID string
	if err := a.database.QueryRow(`SELECT t.id FROM event_teams t JOIN events e ON e.id = t.event_id WHERE e.slug = $1 AND t.slug = $2`, slug, alphaTeam).Scan(&alphaID); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"team_ids": []string{alphaID}, "text": "Please update your README."}
	wantTS(t, "organiser messages Alpha", a.api(t, "POST", base+"/team-messages", body, org.key), http.StatusCreated, "")
	if hist := a.api(t, "GET", base+"/team/messages", nil, alphaLead.key).json(t)["messages"].([]any); len(hist) != 1 {
		t.Fatalf("Alpha history: %v", hist)
	}
	if hist := a.api(t, "GET", base+"/team/messages", nil, betaLead.key).json(t)["messages"].([]any); len(hist) != 0 {
		t.Fatalf("Beta sees Alpha's message: %v", hist)
	}
	if hist := a.api(t, "GET", base+"/teams/"+betaTeam+"/messages", nil, org.key).json(t)["messages"].([]any); len(hist) != 0 {
		t.Fatalf("organiser's Beta history: %v", hist)
	}
	wantTS(t, "participant refused", a.api(t, "POST", base+"/team-messages", body, alphaLead.key), http.StatusNotFound, "event_not_found")
	wantTS(t, "judge refused", a.api(t, "POST", base+"/team-messages", body, judge.key), http.StatusNotFound, "event_not_found")
}
