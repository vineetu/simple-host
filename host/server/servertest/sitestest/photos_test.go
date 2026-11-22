package sitestest

// The photo route (POST /v1/sites/{sitename}/photos) is Sites': it adds
// photos to a site as a new version. What a photo may be (type, size,
// metadata) is host/sites/photos' and host/web's unit tests; the photo and
// upload links that reach this route are accesstest's.

import (
	"bytes"
	"image"
	"image/jpeg"
	"mime/multipart"
	"testing"

	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

// photoJPEG is a blank w×h JPEG.
func photoJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// addPhotos posts one photo named name to p's site with key (p's when "").
func (a *App) addPhotos(t *testing.T, key, site, name string, photo []byte) rt.Resp {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("photos", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(photo)
	_ = mw.Close()
	h := map[string]string{"Content-Type": mw.FormDataContentType()}
	if key != "" {
		h["X-API-Key"] = key
	}
	return a.At(t, "POST", rt.SiteDomain, "/v1/sites/"+site+"/photos", &body, h)
}

// The owner's photo becomes a new version that keeps the site's files and
// is served on the site's address; nobody else may add one.
func TestPhotosAddedAndServed(t *testing.T) {
	a := NewApp(t)
	p, other := a.NewPerson(t, "photos"), a.NewPerson(t, "photos-other")
	a.Deploy(t, p, "home")
	jpg := photoJPEG(t, 8, 6)

	if r := a.addPhotos(t, "", "home", "front.jpg", jpg); r.Status != 401 {
		t.Fatalf("signed out: %d %s", r.Status, r.Body)
	}
	if r := a.addPhotos(t, other.Key, "home", "front.jpg", jpg); r.Status != 404 {
		t.Fatalf("another account's key: %d %s", r.Status, r.Body)
	}
	r := a.addPhotos(t, p.Key, "home", "front.jpg", jpg)
	if r.Status != 200 || r.JSON(t)["version"] != float64(2) {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	if path := r.JSON(t)["photos"].([]any)[0].(map[string]any)["path"]; path != "images/front.jpg" {
		t.Fatalf("path: %v", path)
	}
	// The same name again is suffixed, never overwrites.
	if r := a.addPhotos(t, p.Key, "home", "front.jpg", jpg); r.Status != 200 || r.JSON(t)["photos"].([]any)[0].(map[string]any)["path"] != "images/front-2.jpg" {
		t.Fatalf("second save: %d %s", r.Status, r.Body)
	}
	host := p.SiteHost("home")
	if r := a.At(t, "GET", host, "/images/front.jpg", nil, nil); r.Status != 200 || !bytes.Equal(r.Body, jpg) {
		t.Fatalf("served photo: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>home</h1>" {
		t.Fatalf("kept page: %d %s", r.Status, r.Body)
	}
}
