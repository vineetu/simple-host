package server

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
	key := a.teamKey(t, slug, alphaP)
	if r := a.deployTeam(t, alphaSlug, key, "Alpha site"); r.status != http.StatusOK && r.status != http.StatusCreated {
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

// entryOf finds row["entry"] for a team id in a raw snapshot array (the
// publish response's own "results" field, or GET .../results' admin view).
func entryOf(rows []any, teamID string) map[string]any {
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok || m["team_id"] != teamID {
			continue
		}
		e, _ := m["entry"].(map[string]any)
		return e
	}
	return nil
}

// TestHackResultsCardsSnapshotAndPublicVisibility covers build-plan §8
// "Results": the snapshot holds a card for every ranked team (even one a
// winners-only public view won't show), winners-only hides the non-winner's
// card on every public surface (results JSON, public JSON, the rendered
// page, and the team's own /my-results), a live site's URL appears, an
// unsited team's doesn't, and the Off code rule empties code_url.
func TestHackResultsCardsSnapshotAndPublicVisibility(t *testing.T) {
	w := newCardWorld(t)
	pub := w.publish(t)
	if pub.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	rows := pub.json(t)["results"].([]any)

	alphaCard := entryOf(rows, w.alphaID)
	if alphaCard == nil {
		t.Fatal("snapshot has no card for the winning team")
	}
	if alphaCard["title"] != "CareCompass" || alphaCard["problem"] != "UNIQUE_ALPHA_PROBLEM families cannot find the right clinic" {
		t.Fatalf("alpha card fields: %+v", alphaCard)
	}
	if alphaCard["code_url"] != "" {
		t.Fatalf("code_url should be empty (rule Off): %+v", alphaCard)
	}
	if alphaCard["video_url"] != "https://youtu.be/dQw4w9WgXcQ" || alphaCard["video_host"] != "youtu.be" {
		t.Fatalf("alpha video: %+v", alphaCard)
	}
	wantSiteURL := "https://" + w.alphaSlug + "." + w.slug + "." + tsDomain + "/"
	if alphaCard["site_url"] != wantSiteURL {
		t.Fatalf("alpha site_url: got %v want %v", alphaCard["site_url"], wantSiteURL)
	}

	// The snapshot holds every ranked team's card, including Beta's, even
	// though Beta is not a public winner -- the organiser-facing publish
	// response is not itself filtered by visibility.
	betaCard := entryOf(rows, w.betaID)
	if betaCard == nil {
		t.Fatal("snapshot has no card for the non-winning team")
	}
	if betaCard["site_url"] != "" {
		t.Fatalf("beta never had a site, want no site_url: %+v", betaCard)
	}

	// Public /results, winners-only by default: Beta's card and its unique
	// problem text must not leak anywhere in the response.
	publicRes := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/results", nil, "")
	if publicRes.status != http.StatusOK {
		t.Fatalf("public results: %d %s", publicRes.status, publicRes.body)
	}
	if strings.Contains(string(publicRes.body), "UNIQUE_BETA") {
		t.Fatalf("winners-only /results leaked the non-winner's card: %s", publicRes.body)
	}
	winners := publicRes.json(t)["winners"].([]any)
	if len(winners) != 1 {
		t.Fatalf("winners: %v", winners)
	}
	winnerEntry, _ := winners[0].(map[string]any)["entry"].(map[string]any)
	if winnerEntry == nil || winnerEntry["problem"] != "UNIQUE_ALPHA_PROBLEM families cannot find the right clinic" {
		t.Fatalf("winner's own card missing from /results: %v", winners[0])
	}

	// GET .../public (hack_public_json.go) -- the same card, same leak check.
	publicJSON := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/public", nil, "")
	if publicJSON.status != http.StatusOK {
		t.Fatalf("public json: %d %s", publicJSON.status, publicJSON.body)
	}
	if strings.Contains(string(publicJSON.body), "UNIQUE_BETA") {
		t.Fatalf("GET .../public leaked the non-winner's card: %s", publicJSON.body)
	}
	if !strings.Contains(string(publicJSON.body), "UNIQUE_ALPHA_PROBLEM") {
		t.Fatalf("GET .../public missing the winner's card: %s", publicJSON.body)
	}

	// The rendered public page: Alpha's card (with Open website) is there in
	// full; Beta is nowhere.
	page := w.a.on(t, "GET", w.host, "/", nil, "")
	if page.status != http.StatusOK {
		t.Fatalf("page: %d %s", page.status, page.body)
	}
	body := string(page.body)
	if strings.Contains(body, "UNIQUE_BETA") {
		t.Fatalf("public page leaked the non-winner's card")
	}
	if !strings.Contains(body, "UNIQUE_ALPHA_PROBLEM") {
		t.Fatalf("public page missing the winner's problem line")
	}
	if !strings.Contains(body, `href="`+wantSiteURL+`"`) || !strings.Contains(body, `rel="noopener noreferrer nofollow ugc"`) {
		t.Fatalf("public page missing the site link or its rel attribute")
	}
	if strings.Contains(body, "github.com/alpha/repo") {
		t.Fatal("public page shows a code link the event's rules turned Off")
	}

	// my-results: the winner sees shown_publicly=true and its own card;
	// the non-winner sees shown_publicly=false and no card, so the screen
	// can say plainly it isn't public (participant-results.js).
	mineAlpha := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/my-results", nil, w.alphaP.key)
	if mineAlpha.json(t)["shown_publicly"] != true {
		t.Fatalf("alpha shown_publicly: %v", mineAlpha.json(t))
	}
	if entry, _ := mineAlpha.json(t)["entry"].(map[string]any); entry == nil || entry["problem"] != "UNIQUE_ALPHA_PROBLEM families cannot find the right clinic" {
		t.Fatalf("alpha my-results entry: %v", mineAlpha.json(t)["entry"])
	}
	mineBeta := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/my-results", nil, w.betaP.key)
	if mineBeta.json(t)["shown_publicly"] != false {
		t.Fatalf("beta shown_publicly: %v", mineBeta.json(t))
	}
	if mineBeta.json(t)["entry"] != nil {
		t.Fatalf("beta's own my-results should not carry a card while not shown publicly: %v", mineBeta.json(t)["entry"])
	}
}

// TestHackResultsCardsFullRankingTogglePicksUpWithoutRepublish covers
// build-plan §4.6 "Filtering at render time": switching an already-
// published event to full ranking must show every ranked team's card
// immediately, reading the one snapshot already stored -- no republish.
func TestHackResultsCardsFullRankingTogglePicksUpWithoutRepublish(t *testing.T) {
	w := newCardWorld(t)
	if r := w.publish(t); r.status != http.StatusOK {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	if r := w.a.api(t, "PATCH", "/v1/hack/events/"+w.slug+"/results", map[string]any{"full_ranking": true}, w.org.key); r.status != http.StatusOK {
		t.Fatalf("patch full_ranking: %d %s", r.status, r.body)
	}
	full := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/results", nil, "")
	ranking := full.json(t)["ranking"].([]any)
	if len(ranking) != 2 {
		t.Fatalf("ranking: %v", ranking)
	}
	betaEntry, _ := ranking[1].(map[string]any)["entry"].(map[string]any)
	if betaEntry == nil || betaEntry["problem"] != "UNIQUE_BETA_PROBLEM nobody knows which street is lit" {
		// Order is by rank, so index 1 should be Beta (lower score); fall
		// back to a scan in case dense ranking ever ties them.
		found := false
		for _, row := range ranking {
			if e, _ := row.(map[string]any)["entry"].(map[string]any); e != nil && e["problem"] == "UNIQUE_BETA_PROBLEM nobody knows which street is lit" {
				found = true
			}
		}
		if !found {
			t.Fatalf("full ranking did not pick up the non-winner's card without a republish: %v", ranking)
		}
	}

	mineBeta := w.a.api(t, "GET", "/v1/hack/events/"+w.slug+"/my-results", nil, w.betaP.key)
	if mineBeta.json(t)["shown_publicly"] != true {
		t.Fatalf("beta shown_publicly after full_ranking: %v", mineBeta.json(t))
	}
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
