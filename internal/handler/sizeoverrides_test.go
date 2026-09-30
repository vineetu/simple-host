package handler

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/tarball"
)

// withSiteLimit sets MAX_ARCHIVE_MB for one test and puts it back.
func withSiteLimit(t *testing.T, mb int) {
	t.Helper()
	old := SiteLimit()
	restore := tarball.SnapshotLimits()
	SetSiteLimit(int64(mb) << 20)
	t.Cleanup(func() { restore(); maxSiteArchiveSize = old })
}

// tarGzOf is a gzipped tar holding index.html plus a file of n zero bytes:
// a few KB on the wire, n bytes unpacked.
func tarGzOf(t *testing.T, n int) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range map[string][]byte{"index.html": []byte("<h1>big</h1>"), "big.bin": make([]byte, n)} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// zipOf is tarGzOf as a zip.
func zipOf(t *testing.T, n int) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{"index.html": []byte("<h1>big</h1>"), "big.bin": make([]byte, n)} {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// MAX_ARCHIVE_MB_OVERRIDES gives the listed accounts their own per-site cap
// on every path that adds files to a site (archive and JSON, create, update
// and PUT ?create=1), follows an earlier handle, is what /v1/me and the admin
// list report, and never takes down a site that is already larger.
func TestArchiveOverridesEnforced(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	oliveID, oliveHandle := a.userID(t, olive)
	withSiteLimit(t, 1)
	withLimits(t, map[string]string{"MAX_ARCHIVE_MB_OVERRIDES": oliveHandle + ":3"})

	const mb = 1 << 20
	key := func(p person) map[string]string { return map[string]string{"X-API-Key": p.key} }
	send := func(p person, method, path, body string) resp {
		return a.at(t, method, "simple-host.test", path, body, key(p))
	}
	jsonFiles := func(n int) string {
		return `{"files":{"index.html":"<h1>big</h1>","big.txt":"` + strings.Repeat("a", n) + `"}}`
	}
	ok := func(r resp) bool { return r.status == http.StatusCreated || r.status == http.StatusOK }
	tooLarge := func(r resp, limitMB string) bool {
		return r.status == http.StatusRequestEntityTooLarge && strings.Contains(string(r.body), "site_too_large") &&
			strings.Contains(string(r.body), "at most "+limitMB+" MB")
	}
	uploads := []struct {
		name         string
		method, path string
		body         func(n int) string
	}{
		{"POST tar.gz", "POST", "/v1/sites/%s", func(n int) string { return tarGzOf(t, n) }},
		{"POST zip", "POST", "/v1/sites/%s", func(n int) string { return zipOf(t, n) }},
		{"POST files", "POST", "/v1/sites/%s/files", jsonFiles},
		{"PUT ?create=1 tar.gz", "PUT", "/v1/sites/%s?create=1", func(n int) string { return tarGzOf(t, n) }},
		{"PUT ?create=1 files", "PUT", "/v1/sites/%s/files?create=1", jsonFiles},
	}
	for i, u := range uploads {
		site := "s" + string(rune('a'+i))
		path := strings.Replace(u.path, "%s", site, 1)
		// No override: 1 MB.
		if r := send(oscar, u.method, path, u.body(3*mb/2)); !tooLarge(r, "1") {
			t.Errorf("%s: oscar 1.5 MB = %d %s", u.name, r.status, r.body)
		}
		// Override: 3 MB. 1.5 MB goes through, 3.5 MB does not.
		if r := send(olive, u.method, path, u.body(3*mb/2)); !ok(r) {
			t.Errorf("%s: olive 1.5 MB = %d %s", u.name, r.status, r.body)
		}
		if r := send(olive, "PUT", strings.TrimSuffix(path, "?create=1"), u.body(7*mb/2)); !tooLarge(r, "3") {
			t.Errorf("%s: olive update 3.5 MB = %d %s", u.name, r.status, r.body)
		}
	}
	// Updates (PUT archive and PUT files) of an existing site, without an override.
	a.deploy(t, oscar, "small")
	if r := send(oscar, "PUT", "/v1/sites/small", tarGzOf(t, 3*mb/2)); !tooLarge(r, "1") {
		t.Errorf("oscar PUT archive 1.5 MB = %d %s", r.status, r.body)
	}
	if r := send(oscar, "PUT", "/v1/sites/small/files", jsonFiles(3*mb/2)); !tooLarge(r, "1") {
		t.Errorf("oscar PUT files 1.5 MB = %d %s", r.status, r.body)
	}
	if r := send(oscar, "PUT", "/v1/sites/small", tarGzOf(t, mb/2)); !ok(r) {
		t.Errorf("oscar PUT archive 0.5 MB = %d %s", r.status, r.body)
	}

	// Reported: /v1/me for each, and the admin list.
	me := func(p person) any {
		return a.at(t, "GET", "simple-host.test", "/v1/me", nil, key(p)).json(t)["max_site_mb"]
	}
	if got := me(olive); got != float64(3) {
		t.Errorf("olive max_site_mb = %v, want 3", got)
	}
	if got := me(oscar); got != float64(1) {
		t.Errorf("oscar max_site_mb = %v, want 1", got)
	}
	if got := a.at(t, "GET", "simple-host.test", "/v1/me", nil, map[string]string{"X-API-Key": a.admin}).json(t)["max_site_mb"]; got != float64(1) {
		t.Errorf("admin max_site_mb = %v, want 1", got)
	}
	users := a.at(t, "GET", "simple-host.test", "/v1/admin/users", nil, map[string]string{"X-API-Key": a.admin})
	if users.status != http.StatusOK {
		t.Fatalf("admin users: %d %s", users.status, users.body)
	}
	seen := map[string]any{}
	for _, u := range users.json(t)["users"].([]any) {
		m := u.(map[string]any)
		seen[m["id"].(string)] = m["max_site_mb"]
	}
	if seen[oliveID] != float64(3) {
		t.Errorf("admin list: olive max_site_mb = %v, want 3", seen[oliveID])
	}
	oscarID, _ := a.userID(t, oscar)
	if seen[oscarID] != float64(1) {
		t.Errorf("admin list: oscar max_site_mb = %v, want 1", seen[oscarID])
	}

	// The connector's message cap is the largest any account has.
	if got := largestSiteLimit(); got != 3*mb {
		t.Errorf("largestSiteLimit = %d, want %d", got, 3*mb)
	}

	// A handle change keeps the override (the old handle stays an alias).
	if _, err := a.database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, oliveHandle, oliveID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, oliveHandle+"-new", oliveID); err != nil {
		t.Fatal(err)
	}
	if got := me(olive); got != float64(3) {
		t.Errorf("olive max_site_mb after a handle change = %v, want 3", got)
	}
	if _, err := a.database.Exec(`UPDATE users SET handle = $1 WHERE id = $2`, oliveHandle, oliveID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`DELETE FROM handle_aliases WHERE handle = $1`, oliveHandle); err != nil {
		t.Fatal(err)
	}

	// Lowered below a live site: the site stays up, its versions stay, and
	// only a new upload over the new limit is refused.
	ApplyLimits(config.DefaultLimits())
	if got := me(olive); got != float64(1) {
		t.Fatalf("olive max_site_mb without the override = %v, want 1", got)
	}
	host := oliveHandle + "." + pcSiteDomain
	if r := a.at(t, "GET", host, "/sa/big.bin", nil, nil); r.status != http.StatusOK || len(r.body) != 3*mb/2 {
		t.Errorf("larger site after the limit fell: %d, %d bytes", r.status, len(r.body))
	}
	if r := send(olive, "PUT", "/v1/sites/sa", tarGzOf(t, 3*mb/2)); !tooLarge(r, "1") {
		t.Errorf("olive redeploy 1.5 MB without the override = %d %s", r.status, r.body)
	}
	if r := send(olive, "PUT", "/v1/sites/sa", tarGzOf(t, mb/2)); !ok(r) {
		t.Errorf("olive redeploy 0.5 MB = %d %s", r.status, r.body)
	}
	// Rolling back to the larger version adds no bytes, so it is allowed.
	if r := send(olive, "PUT", "/v1/sites/sa/active-version", `{"version_number":1}`); r.status != http.StatusOK {
		t.Errorf("rollback to the larger version: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/sa/big.bin", nil, nil); r.status != http.StatusOK || len(r.body) != 3*mb/2 {
		t.Errorf("larger version after rollback: %d, %d bytes", r.status, len(r.body))
	}

	// The admin sizes: the live copy (what the limit is about) apart from the
	// total on disk, and read again after a change rather than from the cache.
	// The site holds v1 (1.5 MB, live again after the rollback), v2 (0.5 MB)
	// and the live copy, so it is over 1 MB in total and still deployed above.
	usage := func() map[string]any {
		r := a.at(t, "GET", "simple-host.test", "/v1/admin/usage?sizes=1", nil, map[string]string{"X-API-Key": a.admin})
		if r.status != http.StatusOK {
			t.Fatalf("admin usage: %d %s", r.status, r.body)
		}
		for _, x := range r.json(t)["site_sizes"].([]any) {
			m := x.(map[string]any)
			if m["user_id"] == oliveID && m["name"] == "sa" {
				return m
			}
		}
		t.Fatal("sa not in site_sizes")
		return nil
	}
	page := float64(len("<h1>big</h1>"))
	sa := usage()
	if sa["live_bytes"] != float64(3*mb/2)+page || sa["bytes"] != float64(3*mb/2)*2+float64(mb/2)+3*page {
		t.Errorf("sa sizes = live %v, total %v", sa["live_bytes"], sa["bytes"])
	}
	if r := send(olive, "PUT", "/v1/sites/sa", tarGzOf(t, mb/4)); !ok(r) {
		t.Fatalf("olive redeploy 0.25 MB = %d %s", r.status, r.body)
	}
	if sa = usage(); sa["live_bytes"] != float64(mb/4)+page {
		t.Errorf("sa live after a deploy = %v, want the new version's size", sa["live_bytes"])
	}
}

// Without an override the unpacked files stay under the instance's own
// extractor caps, which are wider than MAX_ARCHIVE_MB when it is unset (no
// SetSiteLimit: 500 MB total, 100 MB a file). Only an override narrows them.
func TestArchiveNoOverrideKeepsInstanceUnpackCaps(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	_, oliveHandle := a.userID(t, olive)
	restore := tarball.SnapshotLimits()
	old := maxSiteArchiveSize
	maxSiteArchiveSize = 1 << 20 // the body cap alone, as if SetSiteLimit never ran
	t.Cleanup(func() { restore(); maxSiteArchiveSize = old })
	withLimits(t, map[string]string{"MAX_ARCHIVE_MB_OVERRIDES": oliveHandle + ":1"})

	body := tarGzOf(t, 3<<20/2) // a few KB compressed, 1.5 MB unpacked
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/wide", body, map[string]string{"X-API-Key": oscar.key})
	if r.status != http.StatusCreated {
		t.Fatalf("no override, 1.5 MB unpacked under the instance caps: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "simple-host.test", "/v1/sites/wide", body, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusRequestEntityTooLarge || !strings.Contains(string(r.body), "at most 1 MB") {
		t.Fatalf("override of 1 MB: %d %s", r.status, r.body)
	}
	if statedSiteMB(900<<20) != 500 {
		t.Fatal("a limit above the extractor's ceiling is reported as it is")
	}
}
