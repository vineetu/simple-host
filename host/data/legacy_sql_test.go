package data

import (
	"context"
	"testing"

	model "github.com/vsriram/simple-host/host/model"
)

func TestAuthorizeLegacySQLBeforeExecution(t *testing.T) {
	svc := New(nil, Deps{})
	c := Call{Resource: model.Resource{Kind: "sqlite", Read: "anyone", Write: "anyone", WriteMode: "full", SitePasscode: "inherit"}}
	read := false
	_, err := svc.LegacySQL(context.Background(), LegacySQLInput{Call: c, Mode: "query", Body: watchedReader{&read}})
	if err == nil || err.(*Error).Code != "fixed_routes_required" || read {
		t.Fatalf("authorization: %v, body read: %v", err, read)
	}
}
