package handler

import (
	"context"
	"testing"

	runtime "github.com/vsriram/simple-host/host/storage"
)

// legacyStorageRules: rules a resource can only have if it was saved before
// presets (write anyone with full, or a full-mode SQLite database pages query
// with SQL). Such resources keep working; tests seed them directly.
func legacyStorageRules(kind, read, write, mode string) bool {
	if mode == "" {
		mode = "full"
	}
	x := storageResource{Kind: kind, Read: read, Write: write, WriteMode: mode}
	return mode == "full" && write == "anyone" || x.legacyVisitorSQL()
}

func insertLegacyStorage(t *testing.T, h *SiteHandler, siteID, name, kind, read, write, mode, passcode string) {
	t.Helper()
	if mode == "" {
		mode = "full"
	}
	if passcode == "" {
		passcode = "inherit"
	}
	if _, err := h.database.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,site_passcode,write_mode) VALUES($1,$2,$3,$4,$5,$6,$7)`, siteID, name, kind, read, write, passcode, mode); err != nil {
		t.Fatal(err)
	}
	if kind == "kv" {
		c := storageKVFixtureCall(t, h, siteID, name)
		_, done, err := runtime.OpenRuntimeKV(context.Background(), runtime.RuntimeKVPath(h.storageRuntimeDir(c), name), true, true)
		if err != nil {
			t.Fatal(err)
		}
		done()
	}
}
