package servertest

import (
	"bytes"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"
)

// A photo link saves only into the site it was made for, only from the
// server's own origin (a foreign or opaque "null" origin is refused), never
// into another owner's site, and its token reaches no other route.
func TestRDPhotoLinkOriginAndOwner(t *testing.T) {
	a := NewConnectorApp(t)
	owner, other := a.NewPerson(t, "rd-photo-link"), a.NewPerson(t, "rd-photo-other")
	client := a.RegisterClient(t, TestRedirect)
	access := a.Connect(t, owner, client, TestRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (map[string]any, bool) {
		_, out, failed := ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
		return out, failed
	}
	for _, site := range []string{"blog", "other"} {
		if _, failed := call("create_site", map[string]any{"site": site, "files": map[string]any{"index.html": "<h1>Keep me</h1>"}}); failed {
			t.Fatal("create site")
		}
	}
	// Another owner's site, named like none of the connected account's.
	if r := a.Do(t, "POST", "/v1/sites/private/files", JSONBody(map[string]any{"files": map[string]any{"index.html": "private"}}), map[string]string{"X-API-Key": other.Key, "Content-Type": "application/json"}); r.Status >= 400 {
		t.Fatal("create other owner's site")
	}
	mint := func(site string) string {
		t.Helper()
		out, failed := call("create_upload_link", map[string]any{"site": site})
		if failed {
			t.Fatalf("mint for %s failed", site)
		}
		u, err := url.Parse(out["photos_url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(u.Fragment, "t=")
	}
	var img, body bytes.Buffer
	if err := jpeg.Encode(&img, image.NewRGBA(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("photos", "kitchen.jpg")
	_, _ = part.Write(img.Bytes())
	_ = mw.Close()
	send := func(method, path string, headers map[string]string) Resp {
		return a.Do(t, method, path, bytes.NewReader(body.Bytes()), headers)
	}
	headers := map[string]string{"X-Photo-Token": mint("blog"), "Content-Type": mw.FormDataContentType()}
	for origin, want := range map[string]int{"https://other.example": 403, "null": 403, a.Srv.URL: 200} {
		headers["Origin"] = origin
		if r := send("POST", "/v1/photo-links", headers); r.Status != want {
			t.Fatalf("origin %q: %d %s, want %d", origin, r.Status, r.Body, want)
		}
	}
	headers["Origin"] = a.Srv.URL
	for _, path := range []string{"/v1/sites/other/photos", "/v1/sites/private/photos", "/v1/me", "/v1/sites/blog/files", "/v1/photo-links/other", "/v1/photo-links%2f..%2fsites%2fother%2fphotos"} {
		if r := send("POST", path, headers); r.Status < 400 {
			t.Fatalf("token reached %s: %d", path, r.Status)
		}
	}
	// A query naming another site does not redirect the save.
	if r := send("POST", "/v1/photo-links?site=other&path=/v1/me", headers); r.Status != 200 || r.JSON(t)["site"] != "blog" {
		t.Fatalf("query redirected the save: %d %s", r.Status, r.Body)
	}
	if site, failed := call("get_site", map[string]any{"site": "other"}); failed || strings.Contains(toJSON(site), "images/") {
		t.Fatal("other site changed")
	}
	// A link for another owner's site cannot be minted into a save.
	headers["X-Photo-Token"] = mint("private")
	if r := send("POST", "/v1/photo-links", headers); r.Status < 400 {
		t.Fatalf("another owner's site accepted: %d", r.Status)
	}
}
