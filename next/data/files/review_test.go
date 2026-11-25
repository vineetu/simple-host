package files_test

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"simplehost/core/limits"
	"simplehost/core/testkit"
	"simplehost/data/files"
	datasql "simplehost/data/sql"
)

// Review A3: a _files name the owner wrote with SQL is checked where it is
// used: one that leaves the namespace's folder is missing, never read or deleted.
func TestFileNameCheckedWhereUsed(t *testing.T) {
	k := testkit.Start(t, files.Mount, datasql.Mount)
	eve := k.Person(t, "eve")
	k.Publish(t, eve, "evil", map[string]string{"index.html": "x"})
	victim := filepath.Join(k.App.Config.DataDir, "victim.txt")
	os.WriteFile(victim, []byte("secret"), 0o644)
	key := []string{"X-API-Key", eve.Key}
	base := "https://simple-host.app/v1/sites/evil/storage/"
	testkit.Call(t, k.Browser(), "POST", base+"sqlite/ns/execute", map[string]any{"sql": "SELECT 1"}, key...)
	if r := testkit.Call(t, k.Browser(), "POST", base+"sqlite/ns/execute",
		map[string]any{"sql": "INSERT INTO _files (name, size) VALUES ('../../../../victim.txt', 6)"}, key...); r.Status != 200 {
		t.Fatalf("insert: %d %s", r.Status, r.Body)
	}
	obj := base + "files/ns/objects/" + url.PathEscape("../../../../victim.txt")
	for _, m := range []string{"GET", "DELETE"} {
		if r := testkit.Call(t, k.Browser(), m, obj, nil, key...); r.Status != 404 {
			t.Fatalf("%s outside the namespace: %d %q", m, r.Status, r.Body)
		}
	}
	if b, _ := os.ReadFile(victim); string(b) != "secret" {
		t.Fatal("the file outside the namespace was removed")
	}
}

// Round 2: the file-store cap is measured on disk for the whole site, so
// rewriting _files sizes or using another namespace lets nothing more in.
func TestFileCapIsMeasuredOnDisk(t *testing.T) {
	n := limits.FilesBytes.N
	limits.FilesBytes.N = 1 << 20
	t.Cleanup(func() { limits.FilesBytes.N = n })
	k := testkit.Start(t, files.Mount, datasql.Mount)
	eve := k.Person(t, "eve")
	k.Publish(t, eve, "evil", map[string]string{"index.html": "x"})
	key := []string{"X-API-Key", eve.Key}
	base := "https://simple-host.app/v1/sites/evil/storage/"
	for _, ns := range []string{"ns", "ns2"} {
		testkit.Call(t, k.Browser(), "POST", base+"sqlite/"+ns+"/execute", map[string]any{"sql": "SELECT 1"}, key...)
	}
	big := bytes.Repeat([]byte("a"), 700<<10)
	put := func(path string) int {
		return testkit.Call(t, k.Browser(), "PUT", base+"files/"+path, big, append(key, "Content-Type", "application/octet-stream")...).Status
	}
	if s := put("ns/objects/a"); s != 200 {
		t.Fatalf("first: %d", s)
	}
	testkit.Call(t, k.Browser(), "POST", base+"sqlite/ns/execute", map[string]any{"sql": "UPDATE _files SET size = 0"}, key...)
	for _, p := range []string{"ns/objects/b", "ns2/objects/c"} {
		if s := put(p); s != 413 {
			t.Fatalf("%s over the cap after UPDATE _files: %d", p, s)
		}
	}
	if s := put("ns/objects/a"); s != 200 {
		t.Fatalf("replacing a file: %d", s)
	}
}
