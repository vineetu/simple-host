package lifecycle

import (
	"net/http"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	productconfig "github.com/vsriram/simple-host/products/hack/config"
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
		{name: "active five", maxActive: 5, status: http.StatusConflict, code: "too_many_active_events"},
		{name: "daily five", perDay: 5, status: http.StatusTooManyRequests, code: "too_many_events_today"},
		{name: "admin", perDay: 5, maxActive: 5, admin: true, status: http.StatusCreated},
		{name: "domain exempt", perDay: 5, maxActive: 5, exempt: "domain", status: http.StatusCreated},
		{name: "other domain", perDay: 5, exempt: "@other.example.com", status: http.StatusTooManyRequests, code: "too_many_events_today"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newHackApp(t)
			p := a.NewPerson(t, "limit")
			if tc.admin {
				if _, err := a.DB.Exec(`UPDATE users SET is_admin=true WHERE username=$1`, p.Email); err != nil {
					t.Fatal(err)
				}
			}
			l := productconfig.DefaultLimits()
			l.EventCreatePerDay, l.EventMaxActivePerOrganiser = tc.perDay, tc.maxActive
			switch tc.exempt {
			case "domain":
				l.EventLimitExemptRaw = " @EXAMPLE.COM "
			default:
				l.EventLimitExemptRaw = tc.exempt
			}
			hacktest.SetLimits(a.App, l)
			for i := 0; i < 6; i++ {
				r := a.CreateEvent(t, p, hacktest.UniqueSlug(), nil)
				want := http.StatusCreated
				if i == 5 {
					want = tc.status
				}
				if r.Status != want {
					t.Fatalf("creation %d: %d %s; want %d", i+1, r.Status, r.Body, want)
				}
				if i == 5 && tc.code != "" && r.JSON(t)["code"] != tc.code {
					t.Fatalf("creation %d: %s; want code %s", i+1, r.Body, tc.code)
				}
			}
		})
	}
}
