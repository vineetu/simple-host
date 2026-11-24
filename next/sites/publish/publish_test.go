package publish_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/publish"
	"simplehost/sites/serve"
)

func put(t *testing.T, k *testkit.Kit, key, address string, files map[string]string) testkit.Res {
	t.Helper()
	return testkit.Call(t, k.Browser(), "PUT", address, map[string]any{"files": files}, "Authorization", "Bearer "+key, "Content-Type", "application/json")
}

func get(t *testing.T, k *testkit.Kit, address string) testkit.Res {
	t.Helper()
	return testkit.Call(t, k.Browser(), "GET", address, nil)
}

// SPEC test 6 (F4): a key publishes as its maker. A revoked key is refused at once.
func TestKeyPublishesAsItsMaker(t *testing.T) {
	k := testkit.Start(t, publish.Mount, serve.Mount)
	bob := k.Person(t, "bob")
	r := put(t, k, bob.Key, "https://simple-host.app/v1/sites/blog", map[string]string{"index.html": "hello"})
	if r.Status != 200 || !strings.Contains(r.Body, `"url":"https://blog--bob.simple-host.site/"`) {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}
	if r := get(t, k, "https://blog--bob.simple-host.site/"); r.Status != 200 || r.Body != "hello" {
		t.Fatalf("live: %d %q", r.Status, r.Body)
	}
	var author string
	if err := k.App.DB.QueryRow(`SELECT v.author_id FROM versions v JOIN sites s ON s.id = v.site_id WHERE s.name = 'blog'`).Scan(&author); err != nil || author != bob.ID {
		t.Fatalf("version author %q (%v), want %s", author, err, bob.ID)
	}
	k.Exec(t, `DELETE FROM keys WHERE person_id = $1`, bob.ID) // what revoking does
	r = put(t, k, bob.Key, "https://simple-host.app/v1/sites/blog", map[string]string{"index.html": "changed"})
	if r.Status != 401 || !strings.Contains(r.Body, "bad_credential") {
		t.Fatalf("revoked key: %d %s", r.Status, r.Body)
	}
	if r := get(t, k, "https://blog--bob.simple-host.site/"); r.Body != "hello" {
		t.Fatalf("after refused publish: %q", r.Body)
	}
}

// SPEC test 9 (F6): publish two changed files with keep_other_files. The rest of the site stays.
func TestKeepOtherFiles(t *testing.T) {
	k := testkit.Start(t, publish.Mount, serve.Mount)
	bob := k.Person(t, "bob")
	site := "https://shop--bob.simple-host.site/"
	put(t, k, bob.Key, "https://simple-host.app/v1/sites/shop", map[string]string{"index.html": "home 1", "style.css": "css 1", "about.html": "about 1", "img/logo.svg": "<svg/>"})
	r := put(t, k, bob.Key, "https://simple-host.app/v1/sites/shop?keep_other_files=true", map[string]string{"index.html": "home 2", "style.css": "css 2"})
	if r.Status != 200 || !strings.Contains(r.Body, `"version":2`) {
		t.Fatalf("second publish: %d %s", r.Status, r.Body)
	}
	for path, want := range map[string]string{"": "home 2", "style.css": "css 2", "about": "about 1", "img/logo.svg": "<svg/>"} {
		if r := get(t, k, site+path); r.Status != 200 || r.Body != want {
			t.Fatalf("/%s: %d %q, want %q", path, r.Status, r.Body, want)
		}
	}
}

// SPEC test 10 (F6): a bad upload over a limit is refused whole, naming the
// limit. The live site is unchanged.
func TestOverLimitRefusedWhole(t *testing.T) {
	k := testkit.Start(t, publish.Mount, serve.Mount)
	bob := k.Person(t, "bob")
	put(t, k, bob.Key, "https://simple-host.app/v1/sites/big", map[string]string{"index.html": "small"})

	var archive bytes.Buffer // a few hundred KB that unpack past the 100 MB site limit
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: 5, Typeflag: tar.TypeReg})
	tw.Write([]byte("large"))
	tw.WriteHeader(&tar.Header{Name: "video.bin", Mode: 0o644, Size: 101 << 20, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 101<<20))
	tw.Close()
	gz.Close()
	r := testkit.Call(t, k.Browser(), "PUT", "https://simple-host.app/v1/sites/big", archive.Bytes(), "Authorization", "Bearer "+bob.Key)
	if r.Status != 413 || !strings.Contains(r.Body, "limit_site_size") || !strings.Contains(r.Body, "100 MB") {
		t.Fatalf("over the limit: %d %s", r.Status, r.Body)
	}
	if r := get(t, k, "https://big--bob.simple-host.site/"); r.Body != "small" {
		t.Fatalf("live site changed: %q", r.Body)
	}
	s, _ := k.App.Sites.Get(context.Background(), bob.ID, "big")
	if s.LiveVersion != 1 {
		t.Fatalf("live version %d, want 1", s.LiveVersion)
	}
}
