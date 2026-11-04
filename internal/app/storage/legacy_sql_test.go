package storage

import (
	"context"
	"testing"

	rules "github.com/vsriram/simple-host/internal/rules/storage"
)

func TestAuthorizeLegacySQLBeforeExecution(t *testing.T) {
	svc := New(nil, Deps{PasscodeAllows: func() bool { return false }})
	c := Call{Resource: rules.Resource{Kind: "sqlite", Read: "anyone", Write: "anyone", WriteMode: "full", SitePasscode: "inherit"}}
	auth := svc.AuthorizeLegacySQL(context.Background(), c, "query")
	if auth.Allowed() || auth.Error == nil || auth.Error.Code != "site_locked" {
		t.Fatalf("authorization: %#v", auth)
	}
	if _, err := svc.LegacySQL(context.Background(), LegacySQLInput{Authorization: auth}); err == nil {
		t.Fatal("unauthorized SQL ran")
	}
}
