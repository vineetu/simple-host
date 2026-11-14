package papermetrics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	platform "github.com/vsriram/simple-host/internal/papermetrics"
)

func TestHackMixedKVStorageCounters(t *testing.T) {
	db := isolatedDB(t)
	var owner, site string
	if err := db.QueryRow(`INSERT INTO users(username) VALUES('mixed-holder') RETURNING id`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites(user_id,name) VALUES($1,'team') RETURNING id`, owner).Scan(&site); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO site_storage_resources(site_id,name,kind,kv_store) VALUES($1,'pg','kv','postgres'),($1,'file','kv','sqlite')`, site); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,'pg','key','"value"')`, site); err != nil {
		t.Fatal(err)
	}
	var pgBytes int64
	if err := db.QueryRow(`SELECT pg_column_size(value) FROM site_storage_kv WHERE site_id=$1`, site).Scan(&pgBytes); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"runtime/kv/file.sqlite": strings.Repeat("k", 100), "runtime/sqlite/db.sqlite": "123456789", "runtime/files/uploads/keep": "1234567"} {
		path := filepath.Join(dir, "by-id", owner, "team", name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := Install(platform.New(db, dir))
	e := hackSnapshotEvent{account: owner, id: "event", values: map[string]any{}}
	c.hackSiteSnapshot(context.Background(), e)
	if got := ToInt(e.values["event_storage_bytes"]); got != 16+pgBytes {
		t.Fatalf("historical storage counter=%d want %d", got, 16+pgBytes)
	}
	if got := ToInt(e.values["team_site_bytes_sum"]); got != 116 {
		t.Fatalf("physical bytes=%d", got)
	}
}
