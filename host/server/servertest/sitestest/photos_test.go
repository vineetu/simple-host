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
	"os"
	"path/filepath"
	"strings"
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

// A photo over the account's storage is refused and the live version stays.
func TestPhotosQuotaKeepsLiveVersion(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "photoquota")
	if r := a.files(t, p, "POST", "home", map[string]string{"index.html": strings.Repeat("x", 350000)}); r.Status != 201 {
		t.Fatalf("setup: %d %s", r.Status, r.Body)
	}
	a.SetLimits(t, map[string]string{"MAX_ACCOUNT_MB": "1"})
	if r := a.addPhotos(t, p.Key, "home", "front.jpg", photoJPEG(t, 8, 6)); r.Status != 413 {
		t.Fatalf("quota: %d %s", r.Status, r.Body)
	}
	var active int
	if err := a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, a.SiteID(t, p, "home")).Scan(&active); err != nil || active != 1 {
		t.Fatalf("live version after refusal: %d %v", active, err)
	}
}

// A file where images/ or the photo's path would go is a conflict (409),
// and the file is kept as it was.
func TestPhotoAncestorConflictKeepsFile(t *testing.T) {
	for _, path := range []string{"images", "images/front.jpg/child"} {
		t.Run(path, func(t *testing.T) {
			a := NewApp(t)
			p := a.NewPerson(t, "photoconflict")
			a.Deploy(t, p, "home")
			dest := filepath.Join(a.Disk.VersionDir(p.ID, "home", 1), path)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			if r := a.addPhotos(t, p.Key, "home", "front.jpg", photoJPEG(t, 8, 6)); r.Status != 409 {
				t.Fatalf("conflict: %d %s", r.Status, r.Body)
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != "keep" {
				t.Fatal("conflicting file changed")
			}
		})
	}
}
