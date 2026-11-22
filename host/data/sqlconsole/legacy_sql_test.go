package sqlconsole

import (
	"context"
	"testing"

	"github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/storetest"

	model "github.com/vsriram/simple-host/host/model"
)

func TestAuthorizeLegacySQLBeforeExecution(t *testing.T) {
	svc := data.New(nil, data.Deps{})
	c := data.Call{Resource: model.Resource{Kind: "sqlite", Read: "anyone", Write: "anyone", WriteMode: "full", SitePasscode: "inherit"}}
	read := false
	_, err := LegacySQL(svc, context.Background(), LegacySQLInput{Call: c, Mode: "query", Body: storetest.WatchedReader{Seen: &read}})
	if err == nil || err.(*data.Error).Code != "fixed_routes_required" || read {
		t.Fatalf("authorization: %v, body read: %v", err, read)
	}
}
