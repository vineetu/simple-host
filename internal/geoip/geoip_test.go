package geoip

import (
	"go/build"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/geoip/geoiptest"
)

func writeFixtures(t *testing.T, dir, city, org string) {
	t.Helper()
	if err := geoiptest.Write(filepath.Join(dir, CityFile), "DBIP-City-Lite", map[string]geoiptest.Record{
		"8.8.8.0/24":     geoiptest.CityRecord(city, "United States"),
		"2001:db8::/32":  geoiptest.CityRecord("Sydney", "Australia"),
		"203.0.113.0/24": geoiptest.Record{"country": geoiptest.Record{"names": geoiptest.Record{"en": "Japan"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := geoiptest.Write(filepath.Join(dir, ASNFile), "DBIP-ASN-Lite", map[string]geoiptest.Record{
		"8.8.8.0/24": geoiptest.ASNRecord(15169, org),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLookupFromLocalFiles(t *testing.T) {
	dir := t.TempDir()
	writeFixtures(t, dir, "Mountain View", "Google LLC")
	db := Open(dir)
	defer db.Close()

	cases := []struct {
		ip   string
		want Info
	}{
		{"8.8.8.8", Info{Country: "United States", City: "Mountain View", Org: "Google LLC"}},
		{"::ffff:8.8.8.8", Info{Country: "United States", City: "Mountain View", Org: "Google LLC"}},
		{"2001:db8::1", Info{Country: "Australia", City: "Sydney"}},
		{"203.0.113.9", Info{Country: "Japan"}}, // country-only record
		{"1.1.1.1", Info{}},                     // not in the database
		{"not-an-ip", Info{}},
		{"", Info{}},
	}
	for _, c := range cases {
		if got := db.Lookup(c.ip); got != c.want {
			t.Errorf("Lookup(%q) = %+v, want %+v", c.ip, got, c.want)
		}
	}
}

func TestMissingFilesAreBlank(t *testing.T) {
	db := Open(filepath.Join(t.TempDir(), "does-not-exist"))
	defer db.Close()
	if c, a := db.Loaded(); c || a {
		t.Fatalf("Loaded() = %v, %v with no files", c, a)
	}
	if got := db.Lookup("8.8.8.8"); got != (Info{}) {
		t.Fatalf("Lookup with no database = %+v, want blank", got)
	}
}

func TestCorruptFileIsBlankNotFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, CityFile), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	db := Open(dir)
	defer db.Close()
	if got := db.Lookup("8.8.8.8"); got != (Info{}) {
		t.Fatalf("Lookup with corrupt database = %+v, want blank", got)
	}
	if _, err := Verify(filepath.Join(dir, CityFile)); err == nil {
		t.Fatal("Verify accepted a corrupt file")
	}
}

// The refresh script renames a new file over the old one; Reload must pick it
// up, keep the old data if the replacement is broken, and go blank if the file
// is removed.
func TestHotReload(t *testing.T) {
	dir := t.TempDir()
	writeFixtures(t, dir, "Mountain View", "Google LLC")
	db := Open(dir)
	defer db.Close()

	// Atomic swap, as scripts/geoip-refresh.sh does it.
	tmp := t.TempDir()
	writeFixtures(t, tmp, "Palo Alto", "Google LLC")
	for _, f := range []string{CityFile, ASNFile} {
		if err := os.Rename(filepath.Join(tmp, f), filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	db.Reload()
	if got := db.Lookup("8.8.8.8").City; got != "Palo Alto" {
		t.Fatalf("after swap City = %q, want Palo Alto", got)
	}

	// A broken replacement keeps serving the previous data.
	bad := filepath.Join(tmp, "bad")
	os.WriteFile(bad, []byte("garbage"), 0o644)
	os.Rename(bad, filepath.Join(dir, CityFile))
	db.Reload()
	if got := db.Lookup("8.8.8.8").City; got != "Palo Alto" {
		t.Fatalf("after bad swap City = %q, want previous data kept", got)
	}

	// Removal goes blank (ASN still loaded).
	os.Remove(filepath.Join(dir, CityFile))
	db.Reload()
	if got := db.Lookup("8.8.8.8"); got.City != "" || got.Org != "Google LLC" {
		t.Fatalf("after removal = %+v, want blank city, org kept", got)
	}
}

func TestWatchPicksUpNewFile(t *testing.T) {
	dir := t.TempDir()
	db := Open(dir)
	defer db.Close()
	db.Watch(10 * time.Millisecond)
	writeFixtures(t, dir, "Mountain View", "Google LLC")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if db.Lookup("8.8.8.8").City == "Mountain View" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watcher never loaded the new file")
}

func TestVerify(t *testing.T) {
	dir := t.TempDir()
	writeFixtures(t, dir, "Mountain View", "Google LLC")
	kind, err := Verify(filepath.Join(dir, CityFile))
	if err != nil || kind != "DBIP-City-Lite" {
		t.Fatalf("Verify = %q, %v", kind, err)
	}
}

// This package must never be able to reach the network: a lookup that went
// online would send a caller's IP to a third party.
func TestNoNetworkImports(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if imp == "net/http" || imp == "net/rpc" || strings.HasPrefix(imp, "net/http/") {
			t.Errorf("geoip imports %s; lookups must stay on this box", imp)
		}
	}
	src, _ := os.ReadFile("geoip.go")
	if strings.Contains(string(src), "net.Dial") {
		t.Error("geoip.go dials the network")
	}
}
