package hackhttp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
)

func TestHackEventCreationLimits(t *testing.T) {
	for _, tc := range []struct {
		name              string
		perDay, maxActive int
		exempt            string
		admin             bool
		status            int
		code              string
	}{
		{name: "defaults", perDay: 20, maxActive: 0, status: http.StatusCreated},
		{name: "zero", status: http.StatusCreated},
		{name: "negative", perDay: -1, maxActive: -1, status: http.StatusCreated},
		{name: "active five", maxActive: 5, status: http.StatusConflict, code: "too_many_active_events"},
		{name: "daily five", perDay: 5, status: http.StatusTooManyRequests, code: "too_many_events_today"},
		{name: "admin", perDay: 5, maxActive: 5, admin: true, status: http.StatusCreated},
		{name: "email exempt", perDay: 5, maxActive: 5, exempt: "email", status: http.StatusCreated},
		{name: "domain exempt", perDay: 5, maxActive: 5, exempt: "domain", status: http.StatusCreated},
		{name: "other email", maxActive: 5, exempt: "other@example.com", status: http.StatusConflict, code: "too_many_active_events"},
		{name: "other domain", perDay: 5, exempt: "@other.example.com", status: http.StatusTooManyRequests, code: "too_many_events_today"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newHackApp(t)
			p := a.newPerson(t, "limit")
			if tc.admin {
				if _, err := a.database.Exec(`UPDATE users SET is_admin=true WHERE username=$1`, p.email); err != nil {
					t.Fatal(err)
				}
			}
			l := config.DefaultLimits()
			if tc.name != "defaults" {
				l.EventCreatePerDay, l.EventMaxActivePerOrganiser = tc.perDay, tc.maxActive
			}
			switch tc.exempt {
			case "email":
				l.EventLimitExemptRaw = " " + strings.ToUpper(p.email) + " "
			case "domain":
				l.EventLimitExemptRaw = " @EXAMPLE.COM "
			default:
				l.EventLimitExemptRaw = tc.exempt
			}
			setHackAppLimits(a, l)
			for i := 0; i < 6; i++ {
				r := a.createEvent(t, p, uniqueSlug(), nil)
				want := http.StatusCreated
				if i == 5 {
					want = tc.status
				}
				if r.status != want {
					t.Fatalf("creation %d: %d %s; want %d", i+1, r.status, r.body, want)
				}
				if i == 5 && tc.code != "" && r.json(t)["code"] != tc.code {
					t.Fatalf("creation %d: %s; want code %s", i+1, r.body, tc.code)
				}
			}
		})
	}
}
