package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

func runDemoReset(t *testing.T, envFile string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", "../../deploy/prod/hack-demo-reset.sh")
	// All configuration comes from this fixture, never from a production env file.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HACK_DEMO_ENV=" + envFile}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHackDemoResetScript(t *testing.T) {
	a := newHackApp(t) // httptest selects a free loopback port.
	adminID, err := db.EnsureAdminUser(context.Background(), a.database)
	if err != nil {
		t.Fatal(err)
	}
	previousCreates, err := db.CountEventCreatesSince(context.Background(), a.database, adminID, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	// Four local resets, independent of earlier tests using this same admin row.
	oldLimits := *config.Active()
	limits := oldLimits
	limits.EventCreatePerDay = previousCreates + 4
	config.SetActive(limits)
	t.Cleanup(func() { config.SetActive(oldLimits) })
	testStart := time.Now()
	t.Cleanup(func() {
		_, _ = a.database.Exec(`DELETE FROM event_create_log WHERE user_id=$1 AND created_at >= $2`, adminID, testStart)
	})
	root := uniqueSlug()
	envFile := filepath.Join(t.TempDir(), "demo.env")
	settings := fmt.Sprintf("ADMIN_API_KEY=%s\nHACK_DEMO_BASE=%s\nHACK_DEMO_EVENT_SLUG=%s\n", a.admin, a.srv.URL, root)
	if err := os.WriteFile(envFile, []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	weekYear, week := time.Now().UTC().ISOWeek()
	weekly := fmt.Sprintf("%s-%04d%02d", root, weekYear, week)
	// Clean only these unique fixture events, holding accounts, and retired names.
	t.Cleanup(func() {
		for _, slug := range []string{root, weekly, weekly + "-2"} {
			a.cleanupEvent(slug)
			_, _ = a.database.Exec(`DELETE FROM event_used_names WHERE event_slug=$1`, slug)
		}
	})
	reset := func(wantSlug string) db.Event {
		t.Helper()
		out, err := runDemoReset(t, envFile)
		if strings.Contains(out, a.admin) {
			t.Fatal("reset printed the admin key")
		}
		if err != nil {
			t.Fatalf("reset: %v: %s", err, out)
		}
		ev, err := db.GetEventBySlug(context.Background(), a.database, wantSlug)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "Demo slug: "+wantSlug+"\n") ||
			!strings.Contains(out, "Join URL: "+a.srv.URL+"/join/"+ev.JoinCode+"\n") ||
			!strings.Contains(out, "Judge URL: "+a.srv.URL+"/judge/"+ev.JudgeCode+"\n") {
			t.Fatalf("missing public links or slug: %s", out)
		}
		if ev.Title != "Simple Hack demo" || ev.Stage != "building" || ev.TeamSizeMax != 4 || ev.CocText != "" || ev.JudgingLockedAt.Valid {
			t.Fatalf("unexpected demo settings: title=%q stage=%q team size=%d", ev.Title, ev.Stage, ev.TeamSizeMax)
		}
		if ev.About != "A standing demo you can try as a participant or a judge. It resets every Monday." ||
			!ev.StartsAt.Valid || !ev.EndsAt.Valid || ev.EndsAt.Time.Sub(ev.StartsAt.Time) != 7*24*time.Hour || ev.StartsAt.Time.Weekday() != time.Monday {
			t.Fatal("description or weekly date window differs")
		}
		registration := a.at(t, "GET", "/v1/hack/events/"+wantSlug+"/registration", nil, a.adminH()).json(t)
		if registration["approval_required"] != false || len(registration["questions"].([]any)) != 0 {
			t.Fatal("registration requires approval or answers")
		}
		tracks, err := db.ListEventTracks(context.Background(), a.database, ev.ID)
		if err != nil || len(tracks) != 2 {
			t.Fatal("missing demo tracks")
		}
		trackNames := map[string]bool{}
		for _, track := range tracks {
			trackNames[track.Name] = true
		}
		if !trackNames["Campus life"] || !trackNames["Health"] {
			t.Fatal("unexpected demo tracks")
		}
		rubric, err := db.ListRubric(context.Background(), a.database, ev.ID)
		if err != nil || len(rubric) != 3 {
			t.Fatal("missing demo rubric")
		}
		for i, name := range []string{"Idea", "Usefulness", "Clarity"} {
			if rubric[i].Name != name || rubric[i].MaxPoints != 10 {
				t.Fatal("unexpected criterion or scale")
			}
		}
		if wantSlug != root && !strings.Contains(out, "The demo endpoint must resolve the newest "+root+"-* event.") {
			t.Fatal("missing slug fallback notice")
		}
		return ev
	}
	unused := reset(root)
	t.Log("PASS: demo exists, settings match, and join and judge URLs print")
	old := reset(root)
	if old.ID == unused.ID {
		t.Fatal("unused demo was not replaced")
	}
	t.Log("PASS: consecutive runs replace an unused event and reuse its slug")
	participant, judge := a.newPerson(t, "demo-participant"), a.newPerson(t, "demo-judge")
	for _, join := range []struct {
		path string
		who  person
	}{{"/v1/hack/join/" + old.JoinCode, participant}, {"/v1/hack/judge/" + old.JudgeCode, judge}} {
		r := a.at(t, "POST", join.path, map[string]any{"accept_coc": true, "display_name": "Demo visitor"}, a.key(join.who))
		if r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.body)
		}
	}
	teamSlug := makeEntryTeam(t, a, root, participant, "Old demo team")
	r := a.at(t, "PUT", "/v1/hack/events/"+root+"/entry", map[string]string{"title": "Demo idea", "description": "A useful idea"}, a.key(participant))
	if r.status != 200 {
		t.Fatalf("submit while building: %d %s", r.status, r.body)
	}
	teams, err := db.ListEventTeams(context.Background(), a.database, old.ID)
	if err != nil || len(teams) != 1 || teams[0].Slug != teamSlug {
		t.Fatal("fixture team missing")
	}
	rubric, err := db.ListRubric(context.Background(), a.database, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	r = a.at(t, "PUT", "/v1/hack/events/"+root+"/judge/scores/"+teams[0].ID, map[string]any{
		"scores": []map[string]any{{"criterion_id": rubric[0].ID, "points": 8}},
	}, a.key(judge))
	if r.status != 200 {
		t.Fatalf("score while building: %d %s", r.status, r.body)
	}
	t.Log("PASS: participants join and submit, and judges join and score in building")
	// Confirmation handles even an ended, populated event without a stage workaround.
	r = a.at(t, "POST", "/v1/hack/events/"+root+"/stage", map[string]string{"stage": "archived"}, a.adminH())
	if r.status != 200 {
		t.Fatalf("archive: %d %s", r.status, r.body)
	}
	fresh := reset(weekly)
	if fresh.ID == old.ID {
		t.Fatal("second run kept the old event")
	}
	for _, table := range []string{"events", "event_teams", "event_entries", "event_scores", "event_members"} {
		column := "event_id"
		if table == "events" {
			column = "id"
		}
		var count int
		if err := a.database.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+column+`=$1`, old.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("old %s remains: %d, %v", table, count, err)
		}
	}
	name := a.at(t, "GET", "/v1/hack/names/"+root, nil, a.adminH()).json(t)
	if name["available"] != false || name["code"] != "name_taken" {
		t.Fatal("busy demo slug unexpectedly reusable")
	}
	for _, path := range []string{"/v1/hack/join/" + old.JoinCode, "/v1/hack/judge/" + old.JudgeCode} {
		if r := a.at(t, "GET", path, nil, nil); r.status != 404 {
			t.Fatal("old public link remains usable")
		}
	}
	t.Log("PASS: reset replaces the archived event, erases old teams and scores, and uses the weekly slug")
	// Another participant in the same week reserves the weekly name as well.
	r = a.at(t, "POST", "/v1/hack/join/"+fresh.JoinCode, map[string]any{"accept_coc": true, "display_name": "Returning visitor"}, a.key(participant))
	if r.status != 200 {
		t.Fatalf("join weekly demo: %d %s", r.status, r.body)
	}
	third := reset(weekly + "-2")
	if third.ID == fresh.ID {
		t.Fatal("third run kept the weekly event")
	}
	t.Log("PASS: a repeated reset after another join uses the next free same-week suffix")
}

func TestHackDemoResetFailures(t *testing.T) {
	envFile := filepath.Join(t.TempDir(), "demo.env")
	out, err := runDemoReset(t, envFile)
	if err == nil || !strings.Contains(out, "cannot read") {
		t.Fatal("missing env did not fail clearly")
	}
	const key = "local-demo-test-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != key {
			t.Error("missing admin authentication")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"code":"fixture_unavailable","error":"%s"}`, key)
	}))
	defer srv.Close()
	if err := os.WriteFile(envFile, []byte("ADMIN_API_KEY="+key+"\nHACK_DEMO_BASE="+srv.URL+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = runDemoReset(t, envFile)
	if err == nil || !strings.Contains(out, "HTTP 503") || !strings.Contains(out, "fixture_unavailable") || strings.Contains(out, key) {
		t.Fatal("API failure did not report status and code without the secret")
	}
}
