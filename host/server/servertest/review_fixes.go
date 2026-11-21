package servertest

import (
	"testing"
)

func (a *PrivateApp) SiteIDAny(t *testing.T, uid, name string) string {
	t.Helper()
	var id string
	if err := a.Database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2`, uid, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
