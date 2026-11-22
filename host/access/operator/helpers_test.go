package operator

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// newSignupAddr returns a fresh address with a live dashboard code.
func newSignupAddr(t *testing.T, a *App, label string) (addr, code string) {
	t.Helper()
	addr = label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	code = "515151"
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, code, lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	return addr, code
}

func signupOf(t *testing.T, a *App, addr string) signupRow {
	t.Helper()
	var r signupRow
	if err := a.Database.QueryRow(`SELECT signup_source, signup_agent, signup_method, signup_inferred FROM users WHERE username = $1`, addr).
		Scan(&r.source, &r.agent, &r.method, &r.inferred); err != nil {
		t.Fatal(err)
	}
	return r
}

type signupRow struct {
	source, agent, method sql.NullString
	inferred              bool
}
