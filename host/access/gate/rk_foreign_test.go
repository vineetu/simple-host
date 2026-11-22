package gate

import (
	"net/http"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Another account (and the admin's key, which acts as no one's owner on the
// bare routes) cannot write or delete a site's passcode or remove a named
// viewer; the passcode and the list stay as they were.
func TestRkForeignPasscodeAndViewerWritesRefused(t *testing.T) {
	p := NewPasscodeSite(t)
	other := p.NewPerson(t, "oscar")
	foreign := map[string]map[string]string{"another account": other.Header(), "admin key": {"X-API-Key": p.Admin}}

	if r := p.Lock(t, "goa-2026"); r.Status != http.StatusOK {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	for who, h := range foreign {
		if r := p.At(t, "PUT", rt.SiteDomain, "/v1/sites/trip/lock", map[string]any{"passcode": "hijacked-1"}, h); r.Status != http.StatusNotFound {
			t.Fatalf("%s sets the passcode: %d %s", who, r.Status, r.Body)
		}
		if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/lock", nil, h); r.Status != http.StatusNotFound {
			t.Fatalf("%s removes the passcode: %d %s", who, r.Status, r.Body)
		}
	}
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != http.StatusOK || r.JSON(t)["passcode"] != "goa-2026" {
		t.Fatalf("passcode after the refused writes: %d %s", r.Status, r.Body)
	}

	if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/lock", nil, p.Okey); r.Status != http.StatusOK {
		t.Fatalf("owner removes the passcode: %d %s", r.Status, r.Body)
	}
	if r := p.Grant(t, "mom@example.com"); r.Status != http.StatusOK {
		t.Fatalf("grant: %d %s", r.Status, r.Body)
	}
	for who, h := range foreign {
		if r := p.At(t, "DELETE", rt.SiteDomain, "/v1/sites/trip/viewers/mom@example.com", nil, h); r.Status != http.StatusNotFound {
			t.Fatalf("%s removes a viewer: %d %s", who, r.Status, r.Body)
		}
	}
	if r := p.At(t, "GET", rt.SiteDomain, "/v1/sites/trip/access", nil, p.Okey); !strings.Contains(string(r.Body), "mom@example.com") {
		t.Fatalf("viewer list after the refused removals: %d %s", r.Status, r.Body)
	}
}

func rkMemberCount(t *testing.T, a *App, team string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM team_members m JOIN users u ON u.id = m.team_id WHERE u.handle = $1`, team).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
