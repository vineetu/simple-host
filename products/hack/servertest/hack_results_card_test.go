package servertest

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// Slice 3 (build-plan §4.6, §8 "Results", §9 point 5): the public results
// card (title, tagline, problem, audience, tools, maturity, track, video,
// code, site, screenshot) that flows into the snapshot, the public page
// template, GET .../results, GET .../public and the participant's own
// /my-results. The harness is teamSiteApp (hack_m2_test.go, DB_DSN), since
// this needs both judging and a real team site, not just newJudgingWorld's
// plain hack app.

type cardWorld struct {
	a          *teamSiteApp
	slug, host string
	eventID    string
	accountID  string
	org        person
	alphaP     person // Alpha: wins, has a live site
	betaP      person // Beta: scores lower, never gets a site
	judge      person
	alphaID    string
	betaID     string
	alphaSlug  string
	betaSlug   string
	c1         string // rubric criterion id
}

// insertCardEntry writes an entry directly (same shortcut newJudgingWorld
// and TestHackEventGallery use): this test exercises the results card, not
// the rules engine or PUT /entry's own validation.
func (w *cardWorld) insertCardEntry(t *testing.T, teamID, title, tagline, problem, audience, maturity string, tools []string, videoURL, codeURL string) {
	t.Helper()
	if _, err := w.a.database.Exec(`
		INSERT INTO event_entries (team_id, event_id, title, tagline, problem, audience, maturity, tools, video_url, code_url, submitted_at, submit_settled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())`,
		teamID, w.eventID, title, tagline, problem, audience, maturity, pq.Array(tools), videoURL, codeURL); err != nil {
		t.Fatal(err)
	}
}

func newCardWorld(t *testing.T) *cardWorld {
	t.Helper()
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "cwo")
	alphaP := a.newPerson(t, "cwa")
	betaP := a.newPerson(t, "cwb")
	judgeP := a.newPerson(t, "cwj")
	slug := a.makeEvent(t, org)

	var eventID, accountID string
	if err := a.database.QueryRow(`SELECT id, account_id FROM events WHERE slug = $1`, slug).Scan(&eventID, &accountID); err != nil {
		t.Fatal(err)
	}
	a.join(t, slug, alphaP, org)
	a.join(t, slug, betaP, org)

	// Join as judge: no dedicated teamSiteApp helper, same two calls
	// newJudgingWorld makes against hackApp.
	ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)
	jcode := ev["organiser"].(map[string]any)["judge_code"].(string)
	if r := a.api(t, "POST", "/v1/hack/judge/"+jcode, map[string]any{"accept_coc": true, "display_name": "Jude"}, judgeP.key); r.status != http.StatusOK {
		t.Fatalf("judge join: %d %s", r.status, r.body)
	}

	alphaSlug, _ := a.startTeam(t, slug, "Alpha", alphaP)
	betaSlug, _ := a.startTeam(t, slug, "Beta", betaP)
	var alphaID, betaID string
	if err := a.database.QueryRow(`SELECT id FROM event_teams WHERE event_id=$1 AND slug=$2`, eventID, alphaSlug).Scan(&alphaID); err != nil {
		t.Fatal(err)
	}
	if err := a.database.QueryRow(`SELECT id FROM event_teams WHERE event_id=$1 AND slug=$2`, eventID, betaSlug).Scan(&betaID); err != nil {
		t.Fatal(err)
	}

	w := &cardWorld{
		a: a, slug: slug, host: slug + "." + tsDomain, eventID: eventID, accountID: accountID,
		org: org, alphaP: alphaP, betaP: betaP, judge: judgeP,
		alphaID: alphaID, betaID: betaID, alphaSlug: alphaSlug, betaSlug: betaSlug,
	}

	// code_url is Off for this event (build-plan §1: "code_url only when
	// the event's entry rule for code_url is not off"), set while entry
	// rules are still editable (open, before any stage move).
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/entry-rules", map[string]any{"fields": map[string]string{"code_url": "off"}}, org.key); r.status != http.StatusOK {
		t.Fatalf("entry-rules: %d %s", r.status, r.body)
	}

	w.insertCardEntry(t, alphaID, "CareCompass", "A guide that matches a worry to a clinic.",
		"UNIQUE_ALPHA_PROBLEM families cannot find the right clinic", "UNIQUE_ALPHA_AUDIENCE parents in Jakarta",
		"working", []string{"Claude", "Canva"}, "https://youtu.be/dQw4w9WgXcQ", "https://github.com/alpha/repo")
	w.insertCardEntry(t, betaID, "SafeRoute", "Safer walks home for students.",
		"UNIQUE_BETA_PROBLEM nobody knows which street is lit", "UNIQUE_BETA_AUDIENCE students",
		"concept", []string{"Figma"}, "", "")

	rv := decodeRubric(t, a.api(t, "PUT", "/v1/hack/events/"+slug+"/rubric", rubricBody(criterion("Impact", "", 100, 5)), org.key))
	w.c1 = rv.Criteria[0].ID

	// Team addresses must be "ready" (certs provisioned) before a deploy is
	// accepted at all.
	markReady(t, a.certDir, slug)

	// Alpha gets a real, live team site; Beta never does. Deploying is a
	// generic Simple Host sites-API call, not gated by the hack event
	// stage, so it happens before the stage move below.
	key := alphaP.key
	if r := a.deployTeam(t, slug, alphaSlug, key, "Alpha site"); r.status != http.StatusOK && r.status != http.StatusCreated {
		t.Fatalf("deploy alpha site: %d %s", r.status, r.body)
	}

	// Scoring only opens at judging (scoringStage, hack_judging.go).
	// Use the stage API so due team sites are pinned before publishing results.
	if r := a.api(t, "POST", "/v1/hack/events/"+slug+"/stage", map[string]any{"stage": "judging"}, org.key); r.status != http.StatusOK {
		t.Fatalf("start judging: %d %s", r.status, r.body)
	}

	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+alphaID, map[string]any{"scores": []map[string]any{{"criterion_id": w.c1, "points": 5}}}, judgeP.key); r.status != http.StatusOK {
		t.Fatalf("score alpha: %d %s", r.status, r.body)
	}
	if r := a.api(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+betaID, map[string]any{"scores": []map[string]any{{"criterion_id": w.c1, "points": 1}}}, judgeP.key); r.status != http.StatusOK {
		t.Fatalf("score beta: %d %s", r.status, r.body)
	}
	return w
}

func (w *cardWorld) publish(t *testing.T) resp {
	t.Helper()
	return w.a.api(t, "POST", "/v1/hack/events/"+w.slug+"/results/publish", map[string]any{}, w.org.key)
}

// TestHackResultsCardsTakenDownSiteAndRepublish covers build-plan §4.6
// "Republishing re-snapshots it" together with "site_url only when the
// team site is live AND not taken down": a site that was live at first
// publish and is taken down afterwards must lose its link on the next
// publish, and the rendered page must stop offering it.
func TestHackResultsCardsTakenDownSiteAndRepublish(t *testing.T) {
	w := newCardWorld(t)
	// Beta wins its own track but is not an overall winner. Hide ranks to
	// prove the feed's marker does not depend on public rank visibility.
	var trackID string
	if err := w.a.database.QueryRow(`INSERT INTO event_tracks (event_id, slug, name) VALUES ($1, 'climate', 'Climate') RETURNING id`, w.eventID).Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.a.database.Exec(`UPDATE event_teams SET track_id=$1 WHERE id=$2`, trackID, w.betaID); err != nil {
		t.Fatal(err)
	}
	if r := w.a.api(t, "PATCH", "/v1/hack/events/"+w.slug+"/judging/settings", map[string]any{"public_ranks": false}, w.org.key); r.status != 200 {
		t.Fatalf("hide ranks: %d %s", r.status, r.body)
	}

	if r := w.publish(t); r.status != http.StatusOK {
		t.Fatalf("first publish: %d %s", r.status, r.body)
	}
	first := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/results", nil, "")
	firstCard, _ := first.json(t)["winners"].([]any)[0].(map[string]any)["entry"].(map[string]any)
	if firstCard == nil || firstCard["site_url"] == "" {
		t.Fatalf("first publish should have a live site_url: %v", firstCard)
	}

	if r := w.a.api(t, "POST", "/v1/hack/events/"+w.slug+"/teams/"+w.alphaSlug+"/takedown", map[string]any{"reason": "test"}, w.org.key); r.status != http.StatusOK {
		t.Fatalf("takedown: %d %s", r.status, r.body)
	}
	// Before any republish, the public routes already drop the link.
	early := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/results", nil, "")
	if c, _ := early.json(t)["winners"].([]any)[0].(map[string]any)["entry"].(map[string]any); c == nil || c["site_url"] != "" {
		t.Fatalf("/results still links a taken-down site before republish: %v", c)
	}
	pub := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/public", nil, "")
	publicRows := pub.json(t)["results"].([]any)
	if len(publicRows) != 2 {
		t.Fatalf("want overall and track winners: %v", publicRows)
	}
	for _, raw := range publicRows {
		row := raw.(map[string]any)
		want := row["team_name"] == "Alpha"
		if value, ok := row["overall"].(bool); !ok || value != want {
			t.Fatalf("overall marker: %v", row)
		}
		if !want && row["track_winner"] != true {
			t.Fatalf("Beta must be a track-only winner: %v", row)
		}
		if _, ok := row["rank"]; ok {
			t.Fatalf("hidden rank leaked: %v", row)
		}
	}

	for _, it := range pub.json(t)["results"].([]any) {
		if c, _ := it.(map[string]any)["entry"].(map[string]any); c != nil && c["site_url"] != "" {
			t.Fatalf("/public still links a taken-down site before republish: %v", c)
		}
	}
	if r := w.publish(t); r.status != http.StatusOK {
		t.Fatalf("republish: %d %s", r.status, r.body)
	}
	second := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/results", nil, "")
	secondCard, _ := second.json(t)["winners"].([]any)[0].(map[string]any)["entry"].(map[string]any)
	if secondCard == nil {
		t.Fatal("republish lost the winner's card entirely")
	}
	if secondCard["site_url"] != "" {
		t.Fatalf("taken-down site still has a site_url after republish: %v", secondCard)
	}
	if secondCard["title"] != "CareCompass" {
		t.Fatalf("republish should still carry the rest of the card: %v", secondCard)
	}

	page := w.a.on(t, "GET", w.host, "/", nil, "")
	body := string(page.body)
	if strings.Contains(body, "Open website") {
		t.Fatal("public page still offers a link to a taken-down site")
	}
	if !strings.Contains(body, "CareCompass") {
		t.Fatal("public page lost the rest of the card along with the site link")
	}
}
