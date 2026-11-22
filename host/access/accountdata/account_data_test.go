package accountdata

import (
	"database/sql"
	"net/http"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// Erasing an account retires its handle, but a retired-name row another
// account holds under the same name (possible with namespace checks off)
// stays that account's.
func TestEraseLeavesOthersAlias(t *testing.T) {
	a := NewApp(t)
	p, other := a.NewPerson(t, "leaver"), a.NewPerson(t, "keeper")
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, p.Handle, other.ID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "DELETE", rt.SiteDomain, "/v1/me", map[string]string{"confirm": p.Handle}, p.Header()); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	var holder sql.NullString
	if err := a.Database.QueryRow(`SELECT user_id FROM handle_aliases WHERE handle = $1`, p.Handle).Scan(&holder); err != nil || holder.String != other.ID {
		t.Errorf("another account's alias changed: %v %v", holder, err)
	}
}
