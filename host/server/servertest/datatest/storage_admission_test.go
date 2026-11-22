package datatest

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/data/files"
	"github.com/vsriram/simple-host/host/data/kv"
	"github.com/vsriram/simple-host/host/data/resources"
	"github.com/vsriram/simple-host/host/data/rows"
	"github.com/vsriram/simple-host/host/data/sqlconsole"
	"github.com/vsriram/simple-host/host/data/storetest"
	"github.com/vsriram/simple-host/host/model"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// Each storage kind takes its own admission on a site that holds all of them.
func TestKVAdmissionBesideOtherStorage(t *testing.T) {
	s, c := storetest.KV(t, false)
	ctx := context.Background()
	if _, auth, err := kv.PutKV(s, ctx, kv.KVPutInput{Call: c, Key: "key", Body: strings.NewReader(`{"value":1}`)}); err != nil || auth != nil {
		t.Fatalf("put: %+v %v", auth, err)
	}
	// Owner SQL, row write budget and rules preparation each exercise their
	// designated admission form while the site also has a KV file.
	s.Deps.SQLitePath = filepath.Join(c.RuntimeDir, "sqlite", "rows.sqlite")
	rc := c
	rc.ResourceName = "rows"
	rc.Resource = model.Resource{Kind: "sqlite"}
	_, err := sqlconsole.LegacySQL(s, ctx, sqlconsole.LegacySQLInput{Call: rc, Mode: "schema", Body: strings.NewReader(`{"sql":"CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resources.PutStorageResource(s, ctx, resources.ResourceInput{Call: rc, Name: "rows", Kind: "sqlite", Rules: storetest.Set(ruleengine.Policy{Name: "read", Command: ruleengine.CmdRead, Using: `visitor_id == auth.id || auth.role == "owner"`}, ruleengine.Policy{Name: "add", Command: ruleengine.CmdAdd, Check: `auth.signed_in`}, ruleengine.Policy{Name: "edit", Command: ruleengine.CmdEdit, Using: `visitor_id == auth.id || auth.role == "owner"`}, ruleengine.Policy{Name: "delete", Command: ruleengine.CmdDelete, Using: `visitor_id == auth.id || auth.role == "owner"`})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = rows.TableWrite(s, ctx, rows.TableInput{Call: rc, Table: "notes", Values: map[string]json.RawMessage{"body": json.RawMessage(`"ok"`)}, Authorized: rows.AuthorizeTableWrite(s, ctx, rows.TableInput{Call: rc, Table: "notes"}, "add")})
	if err != nil {
		t.Fatal(err)
	}
	// Upload accounting takes and releases its own admission before staging.
	result, err := resources.PutStorageResource(s, ctx, resources.ResourceInput{Call: c, Name: "uploads", Kind: "files"})
	if err != nil {
		t.Fatal(err)
	}
	upload := c
	upload.ResourceName = "uploads"
	upload.Resource = result.Resource
	s.Deps.FileObjectsLimit = 1000
	if _, auth, err := files.PutFile(s, ctx, files.FilePutInput{Call: upload, Path: "example.txt", Body: strings.NewReader("file")}); err != nil || auth != nil {
		t.Fatalf("mixed upload: %+v %v", auth, err)
	}

}
