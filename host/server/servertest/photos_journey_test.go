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

// (photos) A photo link the connector makes (Access) saves through Sites'
// photo route into the site: the photo is in the new version beside the
// pages already there. The link's rules are accesstest's, the photo route's
// sitestest's.
func TestJourneyPhotoLinkSavesIntoTheSite(t *testing.T) {
	a := NewConnectorApp(t)
	owner := a.NewPerson(t, "photo-journey")
	if r := a.Do(t, "POST", "/v1/sites/blog/files", JSONBody(map[string]any{"files": map[string]string{"index.html": "<h1>Keep me</h1>"}}),
		map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/json"}); r.Status != 201 {
		t.Fatalf("deploy: %d %s", r.Status, r.Body)
	}
	token := a.Connect(t, owner, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	_, out, failed := ToolResultOf(t, a.Rpc(t, token, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog"}}))
	if failed {
		t.Fatal("create_upload_link failed")
	}
	u, err := url.Parse(out["photos_url"].(string))
	if err != nil {
		t.Fatal(err)
	}

	var img, body bytes.Buffer
	if err := jpeg.Encode(&img, image.NewRGBA(image.Rect(0, 0, 8, 6)), nil); err != nil {
		t.Fatal(err)
	}
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("photos", "kitchen.jpg")
	_, _ = part.Write(img.Bytes())
	_ = mw.Close()
	r := a.Do(t, "POST", "/v1/photo-links", &body, map[string]string{"X-Photo-Token": strings.TrimPrefix(u.Fragment, "t="), "Origin": a.Srv.URL, "Content-Type": mw.FormDataContentType()})
	if r.Status != 200 || r.JSON(t)["version"] != float64(2) {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	for path, want := range map[string][]byte{"images/kitchen.jpg": img.Bytes(), "index.html": []byte("<h1>Keep me</h1>")} {
		r := a.Do(t, "GET", "/v1/sites/blog/versions/2/files/"+path, nil, map[string]string{"X-API-Key": owner.Key})
		if r.Status != 200 || !bytes.Equal(r.Body, want) {
			t.Fatalf("version 2 %s: %d", path, r.Status)
		}
	}
}
