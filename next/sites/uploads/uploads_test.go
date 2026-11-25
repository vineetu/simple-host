package uploads_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"simplehost/core/testkit"
	"simplehost/sites/publish"
	"simplehost/sites/uploads"
)

// Integrator (ii, iii): the upload page checks its link when it opens; an
// archive link says it is one, and once used it says so before anything is chosen.
func TestUploadLinkIsCheckedOnOpen(t *testing.T) {
	k := testkit.Start(t, uploads.Mount, publish.Mount)
	bob := k.Person(t, "bob")
	k.Publish(t, bob, "blog", map[string]string{"index.html": "one"})
	r := testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/sites/blog/upload-links", map[string]string{"kind": "archive"}, "X-API-Key", bob.Key)
	var link struct{ URL, Put string }
	if json.Unmarshal([]byte(r.Body), &link); r.Status != 200 {
		t.Fatalf("link: %d %s", r.Status, r.Body)
	}
	check := link.Put // the page asks the same address with GET
	if r := testkit.Call(t, k.Browser(), "GET", check, nil); r.Status != 200 || !strings.Contains(r.Body, `"kind":"archive"`) {
		t.Fatalf("open: %d %s", r.Status, r.Body)
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	f, _ := zw.Create("index.html")
	f.Write([]byte("two"))
	zw.Close()
	if r := testkit.Call(t, k.Browser(), "PUT", link.Put, zb.Bytes(), "Content-Type", "application/zip"); r.Status != 200 {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	if r := testkit.Call(t, k.Browser(), "GET", check, nil); r.Status != 404 || !strings.Contains(r.Body, "link_used") {
		t.Fatalf("open a used link: %d %s", r.Status, r.Body)
	}
}
