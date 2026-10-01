package handler

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

func voteSettings(t *testing.T, a *teamSiteApp, slug string, org person, open, close time.Time, eligibility string) {
	t.Helper()
	path := "/v1/hack/events/" + slug + "/voting"
	r := a.api(t, "PUT", path, map[string]any{
		"enabled": true, "opens_at": open.UTC().Format(time.RFC3339),
		"closes_at": close.UTC().Format(time.RFC3339), "eligibility": eligibility,
	}, org.key)
	wantTS(t, "voting settings", r, 200, "")
}

func TestHackVotingAndDirectory(t *testing.T) {
	a := newTeamSiteApp(t)
	org, otherOrg, p1, p2 := a.newPerson(t, "vote-org"), a.newPerson(t, "vote-other-org"), a.newPerson(t, "vote-p1"), a.newPerson(t, "vote-p2")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	// A draft is never in the public directory; an open event is listed by default.
	dir := a.api(t, "GET", "/v1/hack/directory", nil, "")
	wantTS(t, "directory", dir, 200, "")
	if !strings.Contains(string(dir.body), slug) {
		t.Fatalf("open event missing: %s", dir.body)
	}
	draft := uniqueSlug()
	if r := a.api(t, "POST", "/v1/hack/events", map[string]any{
		"slug": draft, "title": "Hidden draft", "organiser_name": "Org", "contact_email": "org@example.com",
		"purpose": "testing", "expected_participants": 10, "starts_at": "2026-10-01", "time_zone": "UTC",
	}, otherOrg.key); r.status != 201 {
		t.Fatalf("create draft: %d %s", r.status, r.body)
	}
	t.Cleanup(func() {
		var accountID string
		_ = a.database.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, draft).Scan(&accountID)
		_, _ = a.database.Exec(`DELETE FROM events WHERE slug=$1`, draft)
		if accountID != "" {
			_, _ = a.database.Exec(`DELETE FROM users WHERE id=$1`, accountID)
		}
	})
	if got := a.api(t, "GET", "/v1/hack/directory", nil, ""); strings.Contains(string(got.body), draft) {
		t.Fatalf("draft listed: %s", got.body)
	}
	for _, p := range []person{p1, p2} {
		a.join(t, slug, p, org)
	}
	alpha, _ := a.startTeam(t, slug, "Alpha", p1)
	beta, _ := a.startTeam(t, slug, "Beta", p2)
	markReady(t, a.certDir, slug)
	for _, pair := range []struct {
		team string
		p    person
	}{{alpha, p1}, {beta, p2}} {
		key := a.teamKey(t, slug, pair.p)
		r := a.deployTeam(t, pair.team, key, pair.team)
		if r.status != 200 && r.status != 201 {
			t.Fatalf("deploy %s: %d %s", pair.team, r.status, r.body)
		}
	}
	wantTS(t, "gallery open", a.api(t, "PATCH", base, map[string]any{"gallery_open": true}, org.key), 200, "")
	now := time.Now().UTC()
	voteSettings(t, a, slug, org, now.Add(-time.Hour), now.Add(time.Hour), "all_signed_in")
	view := a.api(t, "GET", base+"/vote", nil, "")
	wantTS(t, "public options", view, 200, "")
	if view.json(t)["status"] != "open" || !strings.Contains(string(view.body), alpha) || strings.Contains(string(view.body), "votes") {
		t.Fatalf("public vote response: %s", view.body)
	}
	wantTS(t, "own team", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, p1.key), 409, "own_team_vote")
	selfAliasEmail := strings.Replace(p1.email, "@", "+alias@", 1)
	selfAliasKey, _ := auth.GenerateAPIKey()
	selfAlias, err := db.CreateUser(context.Background(), a.database, selfAliasEmail, selfAliasKey, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, selfAlias.ID) })
	wantTS(t, "alias own team", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, selfAliasKey), 409, "own_team_vote")
	otherSlug := a.makeEvent(t, otherOrg)
	a.join(t, otherSlug, p1, otherOrg)
	foreign, _ := a.startTeam(t, otherSlug, "Foreign", p1)
	wantTS(t, "cross event team", a.api(t, "PUT", base+"/vote", map[string]string{"team": foreign}, p1.key), 404, "team_not_found")

	voters := make([]person, 15)
	for i := range voters {
		voters[i] = a.newPerson(t, "voter")
	}
	for i, p := range voters {
		team := alpha
		if i%2 != 0 {
			team = beta
		}
		wantTS(t, "cast", a.api(t, "PUT", base+"/vote", map[string]string{"team": team}, p.key), 200, "")
	}
	wantTS(t, "same vote", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, voters[0].key), 409, "duplicate_vote")
	changed := a.api(t, "PUT", base+"/vote", map[string]string{"team": beta}, voters[0].key)
	wantTS(t, "change vote", changed, 200, "")
	if changed.json(t)["changed"] != true {
		t.Fatalf("change: %s", changed.body)
	}

	// A second account at the same verified mailbox with a +tag cannot add a vote.
	aliasEmail := strings.Replace(voters[0].email, "@", "+other@", 1)
	aliasKey, _ := auth.GenerateAPIKey()
	alias, err := db.CreateUser(context.Background(), a.database, aliasEmail, aliasKey, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec(`DELETE FROM users WHERE id = $1`, alias.ID) })
	wantTS(t, "alias duplicate", a.api(t, "PUT", base+"/vote", map[string]string{"team": beta}, aliasKey), 409, "duplicate_vote")
	if got := a.api(t, "GET", base+"/my-vote", nil, aliasKey).json(t)["team"]; got != beta {
		t.Fatalf("alias own vote: %v", got)
	}

	// Two concurrent aliases choosing the same target leave exactly one row.
	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i, key := range []string{voters[1].key, voters[1].key} {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			statuses[i] = a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, key).status
		}(i, key)
	}
	wg.Wait()
	if !((statuses[0] == 200 && statuses[1] == 409) || (statuses[0] == 409 && statuses[1] == 200)) {
		t.Fatalf("concurrent duplicate: %v", statuses)
	}
	var count int
	if err := a.database.QueryRow(`SELECT COUNT(*) FROM event_votes v JOIN events e ON e.id=v.event_id WHERE e.slug=$1`, slug).Scan(&count); err != nil || count != 15 {
		t.Fatalf("vote rows: %d %v", count, err)
	}

	// An approved-member-only setting denies an outsider and permits a member.
	voteSettings(t, a, slug, org, now.Add(-time.Hour), now.Add(time.Hour), "event_members")
	wantTS(t, "nonmember denied", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, a.newPerson(t, "out").key), 403, "vote_ineligible")
	wantTS(t, "member allowed", a.api(t, "PUT", base+"/vote", map[string]string{"team": beta}, p1.key), 200, "")
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='pending' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.uid(t, p1)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "pending denied", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, p1.key), 403, "vote_ineligible")
	if _, err := a.database.Exec(`UPDATE event_members SET approval_status='approved' WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND user_id=$2`, slug, a.uid(t, p1)); err != nil {
		t.Fatal(err)
	}
	voteSettings(t, a, slug, org, now.Add(time.Hour), now.Add(2*time.Hour), "participants")
	wantTS(t, "not open yet", a.api(t, "PUT", base+"/vote", map[string]string{"team": beta}, p1.key), 409, "voting_closed")
	voteSettings(t, a, slug, org, now.Add(-time.Hour), now.Add(time.Hour), "participants")
	wantTS(t, "participant allowed", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, p2.key), 200, "")
	wantTS(t, "organiser not participant", a.api(t, "PUT", base+"/vote", map[string]string{"team": alpha}, org.key), 403, "vote_ineligible")

	// Close the window: counts become public and writes stop.
	voteSettings(t, a, slug, org, now.Add(-2*time.Hour), now.Add(-time.Hour), "all_signed_in")
	closed := a.api(t, "GET", base+"/vote", nil, "")
	wantTS(t, "closed results", closed, 200, "")
	if closed.json(t)["status"] != "closed" || !strings.Contains(string(closed.body), "votes") {
		t.Fatalf("closed results: %s", closed.body)
	}
	var tally float64
	for _, row := range closed.json(t)["ranking"].([]any) {
		tally += row.(map[string]any)["votes"].(float64)
	}
	if tally != 17 {
		t.Fatalf("final tally %v, want 17: %s", tally, closed.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET deleted_at=now() WHERE name=$1 AND user_id=(SELECT account_id FROM events WHERE slug=$2)`, alpha, slug); err != nil {
		t.Fatal(err)
	}
	afterCleanup := a.api(t, "GET", base+"/vote", nil, "")
	wantTS(t, "ranking survives site cleanup", afterCleanup, 200, "")
	if got := afterCleanup.json(t)["ranking"].([]any); len(got) != 2 {
		t.Fatalf("final ranking lost removed site: %s", afterCleanup.body)
	}
	wantTS(t, "late vote", a.api(t, "PUT", base+"/vote", map[string]string{"team": beta}, voters[2].key), 409, "voting_closed")
	wantTS(t, "opt out", a.api(t, "PATCH", base+"/directory", map[string]bool{"listed": false}, org.key), 200, "")
	if got := a.api(t, "GET", "/v1/hack/directory", nil, ""); strings.Contains(string(got.body), slug) {
		t.Fatalf("opted-out event listed: %s", got.body)
	}
	wantTS(t, "opt in", a.api(t, "PATCH", base+"/directory", map[string]bool{"listed": true}, org.key), 200, "")
	wantTS(t, "archive", a.api(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	past := a.api(t, "GET", "/v1/hack/directory", nil, "")
	if !strings.Contains(string(past.body), slug) || !strings.Contains(string(past.body), `"past"`) {
		t.Fatalf("past directory: %s", past.body)
	}
	wantTS(t, "archived vote", a.api(t, "PUT", base+"/vote", map[string]string{"team": beta}, voters[3].key), 409, "voting_closed")
	wantTS(t, "platform takedown", a.api(t, "POST", "/v1/admin/hack/events/"+slug+"/takedown", map[string]string{"reason": "abuse"}, a.admin), 200, "")
	if got := a.api(t, "GET", "/v1/hack/directory", nil, ""); strings.Contains(string(got.body), slug) {
		t.Fatalf("taken-down event listed: %s", got.body)
	}
}
