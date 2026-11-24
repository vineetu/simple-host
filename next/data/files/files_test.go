package files_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"simplehost/core/limits"
	"simplehost/core/testkit"
	"simplehost/core/web"
	"simplehost/data/files"
	datarules "simplehost/data/rules"
)

// SPEC test 11 (F7): owner photos through an upload link and a visitor
// upload land in the same file store. Over the namespace size is refused.
func TestOneFileStore(t *testing.T) {
	k := testkit.Start(t, files.Mount, datarules.Mount)
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

	// The owner's photo, the way an upload link stores it (sites/uploads
	// calls this same function).
	req := web.WithCaller(httptest.NewRequest("PUT", "/", nil), web.Caller{ID: bob.ID})
	ns, err := k.App.Data.Open(req, s, "photos")
	if err == nil {
		err = k.App.Data.PutFile(req, ns, "owner/dosa.jpg", "image/jpeg", bob.ID, strings.NewReader("owner photo"))
		ns.Close()
	}
	if err != nil {
		t.Fatal(err)
	}

	page := k.Browser()
	k.SignIn(t, page, ann, k.App.Sites.URL(s))
	objects := k.App.Sites.URL(s) + "v1/sites/gallery/storage/files/photos/objects"
	if r := testkit.Call(t, page, "PUT", objects+"/ann.jpg", "visitor photo", "Content-Type", "image/jpeg", "X-SH-CSRF", "1"); r.Status != 200 {
		t.Fatalf("visitor upload: %d %s", r.Status, r.Body)
	}
	list := testkit.Call(t, k.Browser(), "GET", objects, nil)
	if !strings.Contains(list.Body, `"owner/dosa.jpg"`) || !strings.Contains(list.Body, `"ann.jpg"`) {
		t.Fatalf("one store holds both: %s", list.Body)
	}
	if r := testkit.Call(t, k.Browser(), "GET", objects+"/owner/dosa.jpg", nil); r.Body != "owner photo" || r.Header.Get("Content-Type") != "image/jpeg" {
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
