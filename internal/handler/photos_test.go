package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

func photoJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func photoWebP(t *testing.T, kind string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/photos/" + kind + ".webp")
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func photoChunk(b []byte, kind string, data []byte) []byte {
	out := append([]byte{}, b...)
	out = append(out, []byte(kind)...)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(data)))
	out = append(out, data...)
	if len(data)%2 != 0 {
		out = append(out, 0)
	}
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	return out
}
func photoAPP1(b []byte, payload string) []byte {
	out := append([]byte{}, b[:2]...)
	out = append(out, 255, 225)
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	out = append(out, payload...)
	return append(out, b[2:]...)
}
func photoMultipart(t *testing.T, names []string, data [][]byte) (string, string) {
	t.Helper()
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	for i, name := range names {
		part, err := writer.CreateFormFile("photos", name)
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data[i])
	}
	writer.Close()
	return out.String(), writer.FormDataContentType()
}
func TestPhotoValidation(t *testing.T) {
	jpg := photoJPEG(t, 8, 6)
	webp := photoWebP(t, "lossy")
	for _, kind := range []string{"lossy", "lossless", "alpha"} {
		cfg, ext, err := validatePhoto(photoWebP(t, kind), 2048)
		if err != nil || ext != "webp" || cfg.Width != 8 || cfg.Height != 6 {
			t.Fatalf("%s: %+v %s %v", kind, cfg, ext, err)
		}
	}
	cases := map[string][]byte{
		"text": []byte("not an image"), "svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "html": []byte("<html>hello</html>"),
		"png": {137, 80, 78, 71, 13, 10, 26, 10}, "gif": []byte("GIF89a"),
		"jpeg polyglot":    append(append([]byte{}, jpg...), []byte("<html>hello</html>")...),
		"jpeg header html": append([]byte{255, 216, 255}, []byte("<html>hello</html>")...),
		"jpeg truncated":   jpg[:len(jpg)-3], "webp truncated": webp[:len(webp)-3],
		"jpeg exif": photoAPP1(jpg, "Exif\x00\x00GPS"), "jpeg xmp": photoAPP1(jpg, "http://ns.adobe.com/xap/1.0/\x00GPS"),
		"webp exif": photoChunk(webp, "EXIF", []byte("GPS")), "webp xmp": photoChunk(webp, "XMP ", []byte("GPS")),
		"webp animation": photoChunk(webp, "ANIM", make([]byte, 6)),
		"webp payload":   photoChunk(webp, "HTML", []byte("<html/>")),
		"webp trailing":  append(append([]byte{}, webp...), []byte("<html/>")...),
		"dimensions":     photoJPEG(t, 2049, 1),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := validatePhoto(b, 2048); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestPhotosEndpoint(t *testing.T) {
	a := newPersonApp(t, "off")
	p := a.newPerson(t, "photos")
	other := a.newPerson(t, "otherphotos")
	a.deploy(t, p, "home")
	a.deploy(t, other, "private")
	a.sites.SetPublicBaseURL("https://simple-host.test")
	jpg := photoJPEG(t, 8, 6)
	upload := func(site string, names []string, images [][]byte, key, origin string) resp {
		body, ct := photoMultipart(t, names, images)
		return a.at(t, "POST", "simple-host.test", "/v1/sites/"+site+"/photos", body, map[string]string{"X-API-Key": key, "Origin": origin, "Content-Type": ct})
	}
	for _, tc := range []struct {
		label, site, key, origin string
		want                     int
	}{
		{"signed out", "home", "", "", 401}, {"other owner", "private", p.key, "", 404},
		{"foreign origin", "home", p.key, "https://evil.example", 403}, {"opaque origin", "home", p.key, "null", 403},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := upload(tc.site, []string{"front.jpg"}, [][]byte{jpg}, tc.key, tc.origin)
			if r.status != tc.want {
				t.Fatalf("%d %s", r.status, r.body)
			}
		})
	}
	// An ambient visitor cookie cannot authorize this owner write.
	body, ct := photoMultipart(t, []string{"front.jpg"}, [][]byte{jpg})
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"Cookie": "__Host-sh_vsess=visitor", "Content-Type": ct}); r.status != 401 {
		t.Fatalf("cookie: %d", r.status)
	}
	for _, name := range []string{"../front.jpg", "a/b.jpg", "Front.jpg", "frönt.jpg", "front.png", "front.webp", "a..jpg", ".jpg"} {
		r := upload("home", []string{name}, [][]byte{jpg}, p.key, "")
		if r.status != 400 {
			t.Errorf("name %q: %d %s", name, r.status, r.body)
		}
	}
	for label, b := range map[string][]byte{"html": []byte("<html/>"), "svg": []byte("<svg/>"), "png": {137, 80, 78, 71}, "gif": []byte("GIF89a"), "polyglot": append([]byte{255, 216, 255}, []byte("<html/>")...), "truncated": jpg[:len(jpg)-4], "large dimensions": photoJPEG(t, 2049, 1), "metadata": photoAPP1(jpg, "Exif\x00\x00GPS")} {
		r := upload("home", []string{"front.jpg"}, [][]byte{b}, p.key, "")
		if r.status != 400 {
			t.Errorf("%s: %d %s", label, r.status, r.body)
		}
	}
	r := upload("home", []string{"front.webp"}, [][]byte{photoChunk(photoWebP(t, "lossy"), "EXIF", []byte("GPS"))}, p.key, "")
	if r.status != 400 {
		t.Fatalf("webp metadata: %d", r.status)
	}
	r = upload("home", []string{"front.jpg"}, [][]byte{make([]byte, 1000001)}, p.key, "")
	if r.status != 413 || r.json(t)["code"] != "item_too_large" {
		t.Fatalf("oversize: %d %s", r.status, r.body)
	}
	t.Setenv("PHOTO_MAX_BYTES", "100")
	r = upload("home", []string{"front.jpg"}, [][]byte{jpg}, p.key, "")
	if r.status != 413 || r.json(t)["code"] != "item_too_large" {
		t.Fatalf("custom byte cap: %d", r.status)
	}
	t.Setenv("PHOTO_MAX_BYTES", "1000000")
	t.Setenv("PHOTO_MAX_DIMENSION", "4")
	r = upload("home", []string{"front.jpg"}, [][]byte{jpg}, p.key, "")
	if r.status != 400 {
		t.Fatalf("custom dimension cap: %d", r.status)
	}
	t.Setenv("PHOTO_MAX_DIMENSION", "2048")
	names := make([]string, 31)
	images := make([][]byte, 31)
	for i := range names {
		names[i] = "front.jpg"
		images[i] = jpg
	}
	r = upload("home", names, images, p.key, "")
	if r.status != 400 {
		t.Fatalf("count: %d", r.status)
	}
	r = upload("home", nil, nil, p.key, "")
	if r.status != 400 {
		t.Fatalf("empty: %d", r.status)
	}
	// Two photos in one request become exactly one version, with collision suffixes.
	r = upload("home", []string{"front.jpg", "front.jpg"}, [][]byte{jpg, jpg}, p.key, "https://simple-host.test")
	if r.status != 200 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	result := r.json(t)
	if result["version"] != float64(2) {
		t.Fatalf("version: %v", result)
	}
	saved := result["photos"].([]any)
	uid, handle := a.userID(t, p)
	for i, path := range []string{"images/front.jpg", "images/front-2.jpg"} {
		photo := saved[i].(map[string]any)
		if photo["path"] != path || photo["url"] != "https://"+pcContentHost+"/"+handle+"/home/"+path || photo["bytes"] != float64(len(jpg)) || photo["width"] != float64(8) || photo["height"] != float64(6) {
			t.Fatalf("photo: %v", photo)
		}
		got, err := os.ReadFile(filepath.Join(a.sites.disk.VersionDir(uid, "home", 2), path))
		if err != nil || !bytes.Equal(got, jpg) {
			t.Fatalf("saved bytes: %v", err)
		}
	}
	for _, version := range []int{1, 2} {
		b, err := os.ReadFile(filepath.Join(a.sites.disk.VersionDir(uid, "home", version), "index.html"))
		if err != nil || string(b) != "<h1>home</h1>" {
			t.Fatalf("kept index v%d: %s %v", version, b, err)
		}
		b, err = os.ReadFile(filepath.Join(a.sites.disk.VersionDir(uid, "home", version), "sub/index.html"))
		if err != nil || string(b) != "sub" {
			t.Fatalf("kept sub v%d: %s %v", version, b, err)
		}
	}
	r = upload("home", []string{"front.jpg"}, [][]byte{jpg}, p.key, "")
	if r.status != 200 || r.json(t)["photos"].([]any)[0].(map[string]any)["path"] != "images/front-3.jpg" {
		t.Fatalf("existing collision: %d %s", r.status, r.body)
	}
	versions, err := db.ListVersionsBySite(context.Background(), a.database, a.siteID(t, p, "home"))
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions: %d %v", len(versions), err)
	}
	// Scope uses the very same deploy allowlist as existing uploads.
	if !auth.DeployKeyAllows("POST /v1/sites/{sitename}/photos") {
		t.Fatal("deploy scope missing")
	}
}

func TestPhotosQuota(t *testing.T) {
	for _, setting := range []string{"MAX_ACCOUNT_MB", "MAX_SITE_TOTAL_MB"} {
		t.Run(setting, func(t *testing.T) {
			a := newPersonApp(t, "off")
			p := a.newPerson(t, "photoquota")
			// Existing version occupies most of the cap; another live copy must fail.
			r := a.at(t, "POST", "simple-host.test", "/v1/sites/home/files", map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 350000)}}, map[string]string{"X-API-Key": p.key})
			if r.status != 201 {
				t.Fatalf("setup: %d", r.status)
			}
			withLimits(t, map[string]string{setting: "1", "SITE_TOTAL_CAP_FROM": "2000-01-01T00:00:00Z"})
			photo := photoJPEG(t, 8, 6)
			body, ct := photoMultipart(t, []string{"front.jpg"}, [][]byte{photo})
			r = a.at(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"X-API-Key": p.key, "Content-Type": ct})
			if r.status != 413 {
				t.Fatalf("quota: %d %s", r.status, r.body)
			}
			uid, _ := a.userID(t, p)
			site, _ := db.GetSiteByUser(context.Background(), a.database, uid, "home")
			if site.ActiveVersion != 1 {
				t.Fatal("quota refusal changed live version")
			}
		})
	}
}

func TestPhotosPage(t *testing.T) {
	mux := chromeTestMux(t)
	r := get(t, mux, "simple-host.app", "/photos?site=home")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "Save to site") || !strings.Contains(r.Body.String(), themeScriptNeedle) || !strings.Contains(r.Body.String(), "/signin.js") {
		t.Fatalf("page: %d", r.Code)
	}
	if r.Header().Get("Cache-Control") != "no-store" || !strings.Contains(r.Header().Get("Content-Security-Policy"), "blob:") {
		t.Fatal("page headers")
	}
	t.Setenv("PHOTO_MAX_BYTES", "200000")
	t.Setenv("PHOTO_MAX_DIMENSION", "1024")
	limited := get(t, mux, "simple-host.app", "/photos")
	if !strings.Contains(limited.Body.String(), `data-max-bytes="200000"`) || !strings.Contains(limited.Body.String(), `data-max-dimension="1024"`) {
		t.Fatal("page did not receive server limits")
	}
	if raw := get(t, mux, "simple-host.app", "/photos.html"); raw.Code != 404 {
		t.Fatalf("raw template: %d", raw.Code)
	}
	for _, file := range []string{"libheif.js", "libheif.wasm", "UTIF.js", "pako.min.js", "decoder.js", "wasm.js"} {
		r := get(t, mux, "simple-host.app", "/photo-codecs/v2/"+file)
		if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "immutable") {
			t.Fatalf("asset %s: %d", file, r.Code)
		}
	}
	old := hackMode
	hackMode = true
	defer func() { hackMode = old }()
	for _, path := range []string{"/photos", "/v1/sites/home/photos"} {
		rec := httptest.NewRecorder()
		photosHostOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("Hack route reached") })).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 404 {
			t.Fatalf("Hack: %d", rec.Code)
		}
	}
}

// Optional real-endpoint browser fixture; credentials are written only to a
// private local file, never to test output.
func TestServePhotosForBrowser(t *testing.T) {
	fixture := os.Getenv("PHOTOS_BROWSER_FIXTURE")
	if fixture == "" {
		t.Skip("PHOTOS_BROWSER_FIXTURE unset")
	}
	a := newPersonApp(t, "off")
	p := a.newPerson(t, "browserphotos")
	a.deploy(t, p, "sunny-homes")
	a.sites.SetPublicBaseURL(a.srv.URL)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	data, _ := json.Marshal(map[string]string{"base": a.srv.URL, "key": p.key})
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(fixture)
	time.Sleep(15 * time.Minute)
}

func TestPhotosPublicURLs(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	p := a.newPerson(t, "photoaddress")
	a.deploy(t, p, "home")
	_, handle := a.userID(t, p)
	markReady(t, dir, handle)
	body, ct := photoMultipart(t, []string{"front.webp"}, [][]byte{photoWebP(t, "lossy")})
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"X-API-Key": p.key, "Content-Type": ct})
	if r.status != 200 {
		t.Fatalf("save: %d %s", r.status, r.body)
	}
	photo := r.json(t)["photos"].([]any)[0].(map[string]any)
	if photo["url"] != "https://home."+handle+"."+pcSiteDomain+"/images/front.webp" {
		t.Fatalf("canonical URL: %v", photo)
	}
}

func TestPhotosWebPExtendedHeader(t *testing.T) {
	original := photoWebP(t, "alpha")
	for _, flag := range []byte{2, 4, 8} {
		b := append([]byte{}, original...)
		b[20] |= flag
		if _, _, err := validatePhoto(b, 2048); err == nil {
			t.Fatalf("accepted VP8X flag %d", flag)
		}
	}
	b := append([]byte{}, original...)
	b[24] = 0 // Contradicts the actual image width.
	if _, _, err := validatePhoto(b, 2048); err == nil {
		t.Fatal("accepted contradictory WebP dimensions")
	}
}

func TestPhotosRejectDuplicateJPEGFrame(t *testing.T) {
	jpg := photoJPEG(t, 8, 6)
	off := bytes.Index(jpg, []byte{255, 192})
	if off < 0 {
		t.Fatal("fixture has no frame header")
	}
	n := 2 + int(binary.BigEndian.Uint16(jpg[off+2:off+4]))
	duplicate := append([]byte{}, jpg[:off]...)
	duplicate = append(duplicate, jpg[off:off+n]...)
	duplicate = append(duplicate, jpg[off:]...)
	// JFIF makes DecodeConfig stop at the first frame, before the duplicate.
	jfif := []byte{255, 224, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	data := append([]byte{}, duplicate[:2]...)
	data = append(data, jfif...)
	data = append(data, duplicate[2:]...)
	if _, err := jpeg.DecodeConfig(bytes.NewReader(data)); err != nil {
		t.Fatalf("fixture must pass header-only decode: %v", err)
	}
	if _, _, err := validatePhoto(data, 2048); err == nil {
		t.Fatal("accepted duplicate JPEG frame")
	}
}
