package files_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/textproto"
	"strings"
	"testing"

	"simplehost/core/limits"
	"simplehost/core/testkit"
	"simplehost/data/files"
	datarules "simplehost/data/rules"
	"simplehost/sites/uploads"
)

// SPEC test 11 (F7): owner photos through an upload link and a visitor
// upload land in the same file store. Over the namespace size is refused.
func TestOneFileStore(t *testing.T) {
	k := testkit.Start(t, files.Mount, datarules.Mount, uploads.Mount)
	bob, ann := k.Person(t, "bob"), k.Person(t, "ann")
	k.Publish(t, bob, "gallery", map[string]string{"index.html": "gallery"})
	ctx := context.Background()
	s, _ := k.App.Sites.Get(ctx, bob.ID, "gallery")
	r := testkit.Call(t, k.Browser(), "PUT", "https://simple-host.app/v1/sites/gallery/storage/photos/rules",
		`{"rules": {"_files": {"policies": [{"name": "read", "command": "read", "using": "true"}, {"name": "add", "command": "add", "check": "auth.signed_in"}]}}}`,
		"Authorization", "Bearer "+bob.Key)
	if r.Status != 200 {
		t.Fatal(r.Body)
	}

	// The owner's photo, through an upload link.
	r = testkit.Call(t, k.Browser(), "POST", "https://simple-host.app/v1/sites/gallery/upload-links", `{"kind": "files", "namespace": "photos"}`,
		"Authorization", "Bearer "+bob.Key)
	var link struct{ Put string }
	if json.Unmarshal([]byte(r.Body), &link); r.Status != 200 {
		t.Fatalf("upload link: %d %s", r.Status, r.Body)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreatePart(textproto.MIMEHeader{"Content-Disposition": {`form-data; name="file"; filename="dosa.jpg"`}, "Content-Type": {"image/jpeg"}})
	part.Write([]byte("owner photo"))
	mw.Close()
	if r := testkit.Call(t, k.Browser(), "PUT", link.Put, body.Bytes(), "Content-Type", mw.FormDataContentType()); r.Status != 200 {
		t.Fatalf("owner upload: %d %s", r.Status, r.Body)
	}

	page := k.Browser()
	k.SignIn(t, page, ann, k.App.Sites.URL(s))
	objects := k.App.Sites.URL(s) + "v1/sites/gallery/storage/files/photos/objects"
	if r := testkit.Call(t, page, "PUT", objects+"/ann.jpg", "visitor photo", "Content-Type", "image/jpeg", "X-SH-CSRF", "1"); r.Status != 200 {
		t.Fatalf("visitor upload: %d %s", r.Status, r.Body)
	}
	list := testkit.Call(t, k.Browser(), "GET", objects, nil)
	if !strings.Contains(list.Body, `"dosa.jpg"`) || !strings.Contains(list.Body, `"ann.jpg"`) {
		t.Fatalf("one store holds both: %s", list.Body)
	}
	if r := testkit.Call(t, k.Browser(), "GET", objects+"/dosa.jpg", nil); r.Body != "owner photo" || r.Header.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("fetch: %d %q", r.Status, r.Body)
	}

	old := limits.FilesBytes.N
	limits.FilesBytes.N = 100
	t.Cleanup(func() { limits.FilesBytes.N = old })
	r = testkit.Call(t, page, "PUT", objects+"/big.jpg", strings.Repeat("x", 200), "X-SH-CSRF", "1")
	if r.Status != 413 || !strings.Contains(r.Body, "limit_files_size") {
		t.Fatalf("over the cap: %d %s", r.Status, r.Body)
	}
	if after := testkit.Call(t, k.Browser(), "GET", objects, nil); strings.Contains(after.Body, "big.jpg") {
		t.Fatalf("refused file was kept: %s", after.Body)
	}
}
