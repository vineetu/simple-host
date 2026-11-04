package storage

import (
	"context"
	"testing"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestAuthorizeLegacySQLBeforeExecution(t *testing.T) {
	svc := New(nil, Deps{PasscodeAllows: func() bool { return false }})
	c := Call{Resource: rules.Resource{Kind: "sqlite", Read: "anyone", Write: "anyone", WriteMode: "full", SitePasscode: "inherit"}}
	read := false
	_, err := svc.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "query", ReadBody: func() ([]byte, error) { read = true; return nil, nil }})
	if err == nil || err.(*Error).Code != "site_locked" || read {
		t.Fatalf("authorization: %v, body read: %v", err, read)
	}
}
