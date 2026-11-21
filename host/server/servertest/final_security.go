package servertest

import (
	"testing"
)

// v0.7.1 security fixes. Need DB_DSN (host/db/schema.sql applied).

func (a *PrivateApp) sessionCount(t *testing.T, uid string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM visitor_sessions WHERE user_id = $1`, uid).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
