package accountdata

import (
	"net/http"
	"strings"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// An account without a handle confirms with its email.
func TestDeleteMyAccountWithoutHandle(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "nohandle")
	if _, err := a.Database.Exec(`UPDATE users SET handle = NULL WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	key := p.Header()
	r := a.At(t, "GET", rt.SiteDomain, "/v1/me/export.zip", nil, key)
	if r.Status != 200 || !strings.Contains(acctFileUnder(t, acctZipFiles(t, r.Body), "/account.json"), p.Email) {
		t.Fatalf("export without a handle: %d", r.Status)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": "nohandle"}, key); r.Status != 400 || r.Code() != "confirm_mismatch" {
		t.Errorf("wrong confirm: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": " " + p.Email + " "}, key); r.Status != http.StatusNoContent {
		t.Fatalf("delete with email: %d %s", r.Status, r.Body)
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM users WHERE id = $1`, p.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("account row left: %d %v", n, err)
	}
}
