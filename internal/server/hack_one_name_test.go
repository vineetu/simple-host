package server

import (
	"net/url"
	"strings"
	"testing"
)

func TestHackOneNameJoins(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "oneorg")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	info := a.api(t, "GET", base, nil, org.key).json(t)["organiser"].(map[string]any)
	check := func(t *testing.T, r resp, name string) {
		t.Helper()
		if r.status != 200 {
			t.Fatalf("join: %d %s", r.status, r.body)
		}
		if got := r.json(t)["me"].(map[string]any)["display_name"]; got != name {
			t.Fatalf("member name = %v, want %q", got, name)
		}
	}
	for _, role := range []string{"join", "judge"} {
		codeKey := "join_code"
		if role == "judge" {
			codeKey = "judge_code"
		}
		path := "/v1/hack/" + role + "/" + info[codeKey].(string)
		t.Run(role, func(t *testing.T) {
			for i, supplied := range []any{nil, "Different name", "\n" + strings.Repeat("x", 101)} {
				p := a.newPerson(t, "named"+role+string(rune('a'+i)))
				if _, err := a.database.Exec(`UPDATE users SET display_name='Account Name' WHERE id=$1`, a.uid(t, p)); err != nil {
					t.Fatal(err)
				}
				preview := a.api(t, "GET", path, nil, p.key).json(t)
				if preview["account_name"] != "Account Name" {
					t.Fatalf("preview: %v", preview)
				}
				body := map[string]any{"accept_coc": true}
				if supplied != nil {
					body["display_name"] = supplied
				}
				check(t, a.api(t, "POST", path, body, p.key), "Account Name")
				var account, event string
				if err := a.database.QueryRow(`SELECT u.display_name,m.display_name FROM users u JOIN event_members m ON m.user_id=u.id JOIN events e ON e.id=m.event_id WHERE u.id=$1 AND e.slug=$2`, a.uid(t, p), slug).Scan(&account, &event); err != nil {
					t.Fatal(err)
				}
				if account != "Account Name" || event != "" {
					t.Fatalf("stored account=%q event=%q", account, event)
				}
			}
			p := a.newPerson(t, "unnamed"+role)
			preview := a.api(t, "GET", path, nil, p.key).json(t)
			if preview["account_name"] != "" {
				t.Fatalf("unnamed preview: %v", preview)
			}
			r := a.api(t, "POST", path, map[string]any{"accept_coc": true}, p.key)
			if r.status != 400 || r.json(t)["code"] != "invalid_display_name" {
				t.Fatalf("missing name: %d %s", r.status, r.body)
			}
			check(t, a.api(t, "POST", path, map[string]any{"accept_coc": true, "display_name": " Asha Rao "}, p.key), "Asha Rao")
			var account string
			if err := a.database.QueryRow(`SELECT display_name FROM users WHERE id=$1`, a.uid(t, p)).Scan(&account); err != nil {
				t.Fatal(err)
			}
			if account != "Asha Rao" {
				t.Fatalf("account = %q", account)
			}
		})
	}
}

func TestHackOneNameApplyInviteAndRename(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "onerenorg")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	if _, err := a.database.Exec(`UPDATE events SET directory_listed=true WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	applicant := a.newPerson(t, "onerenapply")
	r := a.api(t, "POST", base+"/apply", map[string]any{"accept_coc": true, "display_name": "Asha Rao"}, applicant.key)
	if r.status != 200 {
		t.Fatalf("apply: %d %s", r.status, r.body)
	}
	var name string
	if err := a.database.QueryRow(`SELECT display_name FROM users WHERE id=$1`, a.uid(t, applicant)).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Asha Rao" {
		t.Fatalf("apply account=%q", name)
	}
	if got := a.api(t, "GET", base+"/apply", nil, applicant.key).json(t)["account_name"]; got != "Asha Rao" {
		t.Fatalf("apply preview=%v", got)
	}
	// Accept a co-organiser invite without a name for a named account.
	co := a.newPerson(t, "onereco")
	if _, err := a.database.Exec(`UPDATE users SET display_name='Co Account' WHERE id=$1`, a.uid(t, co)); err != nil {
		t.Fatal(err)
	}
	invite := a.api(t, "POST", base+"/organiser-invite", nil, org.key)
	if invite.status != 201 {
		t.Fatalf("invite: %d %s", invite.status, invite.body)
	}
	u, err := url.Parse(invite.json(t)["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/hack" + u.Path
	if got := a.api(t, "GET", path, nil, co.key).json(t)["account_name"]; got != "Co Account" {
		t.Fatalf("invite preview=%v", got)
	}
	if r := a.api(t, "POST", path, map[string]any{"accept_coc": true}, co.key); r.status != 200 {
		t.Fatalf("accept: %d %s", r.status, r.body)
	}
	// Renaming the account is reflected by both people and the existing team.
	p := a.newPerson(t, "onerenpart")
	a.join(t, slug, p, org)
	a.startTeam(t, slug, "One Name Team", p)
	r = a.api(t, "PATCH", "/v1/me", map[string]any{"display_name": "Renamed Account"}, p.key)
	if r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	for _, path := range []string{base + "/people", base + "/teams"} {
		r := a.api(t, "GET", path, nil, org.key)
		if r.status != 200 || !strings.Contains(string(r.body), `"display_name":"Renamed Account"`) {
			t.Fatalf("name in %s: %d %s", path, r.status, r.body)
		}
	}
}

func TestHackOneNameCreator(t *testing.T) {
	a := newTeamSiteApp(t)
	for _, existing := range []string{"", "Account Creator"} {
		org := a.newPerson(t, "onecreator"+uniqueSlug())
		if _, err := a.database.Exec(`UPDATE users SET display_name=$2 WHERE id=$1`, a.uid(t, org), existing); err != nil {
			t.Fatal(err)
		}
		r := a.createEvent(t, org, uniqueSlug(), map[string]any{"organiser_name": "Public Organiser"})
		if r.status != 201 {
			t.Fatalf("create: %d %s", r.status, r.body)
		}
		want := existing
		if want == "" {
			want = "Public Organiser"
		}
		if got := r.json(t)["me"].(map[string]any)["display_name"]; got != want {
			t.Fatalf("creator name=%v, want %q", got, want)
		}
	}
}
