package datatest

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/jpeg"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/server/servertest"

	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
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

func TestPhotosEndpoint(t *testing.T) {
	a := servertest.NewPersonApp(t, "off")
	p := a.NewPerson(t, "photos")
	other := a.NewPerson(t, "otherphotos")
	a.Deploy(t, p, "home")
	a.Deploy(t, other, "private")
	a.Sites.SetPublicBaseURL("https://simple-host.test")
	a.Data.SetPublicBaseURL("https://simple-host.test")
	jpg := photoJPEG(t, 8, 6)
	upload := func(site string, names []string, images [][]byte, key, origin string) servertest.Resp {
		body, ct := photoMultipart(t, names, images)
		return a.At(t, "POST", "simple-host.test", "/v1/sites/"+site+"/photos", body, map[string]string{"X-API-Key": key, "Origin": origin, "Content-Type": ct})
	}
	for _, tc := range []struct {
		label, site, key, origin string
		want                     int
	}{
		{"signed out", "home", "", "", 401}, {"other owner", "private", p.Key, "", 404},
		{"foreign origin", "home", p.Key, "https://evil.example", 403}, {"opaque origin", "home", p.Key, "null", 403},
	} {
		t.Run(tc.label, func(t *testing.T) {
			r := upload(tc.site, []string{"front.jpg"}, [][]byte{jpg}, tc.key, tc.origin)
			if r.Status != tc.want {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
		})
	}
	// An ambient visitor cookie cannot authorize this owner write.
	body, ct := photoMultipart(t, []string{"front.jpg"}, [][]byte{jpg})
	if r := a.At(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"Cookie": "__Host-sh_vsess=visitor", "Content-Type": ct}); r.Status != 401 {
		t.Fatalf("cookie: %d", r.Status)
	}
	for _, name := range []string{"../front.jpg", "a/b.jpg", "Front.jpg", "frönt.jpg", "front.png", "front.webp", "a..jpg", ".jpg"} {
		r := upload("home", []string{name}, [][]byte{jpg}, p.Key, "")
		if r.Status != 400 {
			t.Errorf("name %q: %d %s", name, r.Status, r.Body)
		}
	}
	for label, b := range map[string][]byte{"html": []byte("<html/>"), "svg": []byte("<svg/>"), "png": {137, 80, 78, 71}, "gif": []byte("GIF89a"), "polyglot": append([]byte{255, 216, 255}, []byte("<html/>")...), "truncated": jpg[:len(jpg)-4], "large dimensions": photoJPEG(t, 2049, 1), "metadata": photoAPP1(jpg, "Exif\x00\x00GPS")} {
		r := upload("home", []string{"front.jpg"}, [][]byte{b}, p.Key, "")
		if r.Status != 400 {
			t.Errorf("%s: %d %s", label, r.Status, r.Body)
		}
	}
	r := upload("home", []string{"front.webp"}, [][]byte{photoChunk(photoWebP(t, "lossy"), "EXIF", []byte("GPS"))}, p.Key, "")
	if r.Status != 400 {
		t.Fatalf("webp metadata: %d", r.Status)
	}
	r = upload("home", []string{"front.jpg"}, [][]byte{make([]byte, 1000001)}, p.Key, "")
	if r.Status != 413 || r.JSON(t)["code"] != "item_too_large" {
		t.Fatalf("oversize: %d %s", r.Status, r.Body)
	}
	t.Setenv("PHOTO_MAX_BYTES", "100")
	r = upload("home", []string{"front.jpg"}, [][]byte{jpg}, p.Key, "")
	if r.Status != 413 || r.JSON(t)["code"] != "item_too_large" {
		t.Fatalf("custom byte cap: %d", r.Status)
	}
	t.Setenv("PHOTO_MAX_BYTES", "1000000")
	t.Setenv("PHOTO_MAX_DIMENSION", "4")
	r = upload("home", []string{"front.jpg"}, [][]byte{jpg}, p.Key, "")
	if r.Status != 400 {
		t.Fatalf("custom dimension cap: %d", r.Status)
	}
	t.Setenv("PHOTO_MAX_DIMENSION", "2048")
	names := make([]string, 31)
	images := make([][]byte, 31)
	for i := range names {
		names[i] = "front.jpg"
		images[i] = jpg
	}
	r = upload("home", names, images, p.Key, "")
	if r.Status != 400 {
		t.Fatalf("count: %d", r.Status)
	}
	r = upload("home", nil, nil, p.Key, "")
	if r.Status != 400 {
		t.Fatalf("empty: %d", r.Status)
	}
	// Two photos in one request become exactly one version, with collision suffixes.
	r = upload("home", []string{"front.jpg", "front.jpg"}, [][]byte{jpg, jpg}, p.Key, "https://simple-host.test")
	if r.Status != 200 {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	result := r.JSON(t)
	if result["version"] != float64(2) {
		t.Fatalf("version: %v", result)
	}
	saved := result["photos"].([]any)
	uid, handle := a.UserID(t, p)
	for i, path := range []string{"images/front.jpg", "images/front-2.jpg"} {
		photo := saved[i].(map[string]any)
		if photo["path"] != path || photo["url"] != "https://"+servertest.PcContentHost+"/"+handle+"/home/"+path || photo["bytes"] != float64(len(jpg)) || photo["width"] != float64(8) || photo["height"] != float64(6) {
			t.Fatalf("photo: %v", photo)
		}
		got, err := os.ReadFile(filepath.Join(a.Sites.Disk.VersionDir(uid, "home", 2), path))
		if err != nil || !bytes.Equal(got, jpg) {
			t.Fatalf("saved bytes: %v", err)
		}
	}
	for _, version := range []int{1, 2} {
		b, err := os.ReadFile(filepath.Join(a.Sites.Disk.VersionDir(uid, "home", version), "index.html"))
		if err != nil || string(b) != "<h1>home</h1>" {
			t.Fatalf("kept index v%d: %s %v", version, b, err)
		}
		b, err = os.ReadFile(filepath.Join(a.Sites.Disk.VersionDir(uid, "home", version), "sub/index.html"))
		if err != nil || string(b) != "sub" {
			t.Fatalf("kept sub v%d: %s %v", version, b, err)
		}
	}
	r = upload("home", []string{"front.jpg"}, [][]byte{jpg}, p.Key, "")
	if r.Status != 200 || r.JSON(t)["photos"].([]any)[0].(map[string]any)["path"] != "images/front-3.jpg" {
		t.Fatalf("existing collision: %d %s", r.Status, r.Body)
	}
	versions, err := sitesrepo.ListVersionsBySite(context.Background(), a.Database, a.SiteID(t, p, "home"))
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
			a := servertest.NewPersonApp(t, "off")
			p := a.NewPerson(t, "photoquota")
			// Existing version occupies most of the cap; another live copy must fail.
			r := a.At(t, "POST", "simple-host.test", "/v1/sites/home/files", map[string]any{"files": map[string]string{"index.html": strings.Repeat("x", 350000)}}, map[string]string{"X-API-Key": p.Key})
			if r.Status != 201 {
				t.Fatalf("setup: %d", r.Status)
			}
			servertest.SetAppLimits(a, servertest.WithLimits(t, map[string]string{setting: "1", "SITE_TOTAL_CAP_FROM": "2000-01-01T00:00:00Z"}))
			photo := photoJPEG(t, 8, 6)
			body, ct := photoMultipart(t, []string{"front.jpg"}, [][]byte{photo})
			r = a.At(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"X-API-Key": p.Key, "Content-Type": ct})
			if r.Status != 413 {
				t.Fatalf("quota: %d %s", r.Status, r.Body)
			}
			uid, _ := a.UserID(t, p)
			site, _ := sitesrepo.GetSiteByUser(context.Background(), a.Database, uid, "home")
			if site.ActiveVersion != 1 {
				t.Fatal("quota refusal changed live version")
			}
		})
	}
}

// Optional real-endpoint browser fixture; credentials are written only to a
// private local file, never to test output.
func TestServePhotosForBrowser(t *testing.T) {
	fixture := os.Getenv("PHOTOS_BROWSER_FIXTURE")
	if fixture == "" {
		t.Skip("PHOTOS_BROWSER_FIXTURE unset")
	}
	a := servertest.NewPersonApp(t, "off")
	p := a.NewPerson(t, "browserphotos")
	a.Deploy(t, p, "sunny-homes")
	a.Sites.SetPublicBaseURL(a.Srv.URL)
	a.Data.SetPublicBaseURL(a.Srv.URL)
	siteshttp.RegisterUIRoutes(a.Mux, a.Srv.URL, a.Sites)
	data, _ := json.Marshal(map[string]string{"base": a.Srv.URL, "key": p.Key})
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(fixture)
	time.Sleep(15 * time.Minute)
}

func TestPhotosPublicURLs(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	p := a.NewPerson(t, "photoaddress")
	a.Deploy(t, p, "home")
	_, handle := a.UserID(t, p)
	servertest.MarkReady(t, dir, handle)
	body, ct := photoMultipart(t, []string{"front.webp"}, [][]byte{photoWebP(t, "lossy")})
	r := a.At(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"X-API-Key": p.Key, "Content-Type": ct})
	if r.Status != 200 {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	photo := r.JSON(t)["photos"].([]any)[0].(map[string]any)
	if photo["url"] != "https://home."+handle+"."+servertest.PcSiteDomain+"/images/front.webp" {
		t.Fatalf("canonical URL: %v", photo)
	}
}

func TestPhotoReconcile(t *testing.T) {
	a := servertest.NewPersonApp(t, "off")
	p := a.NewPerson(t, "reconcile")
	a.Deploy(t, p, "home")
	jpg := photoJPEG(t, 8, 6)
	send := func(names []string, images [][]byte, reconcile bool) servertest.Resp {
		body, ct := photoMultipart(t, names, images)
		headers := map[string]string{"X-API-Key": p.Key, "Content-Type": ct}
		if reconcile {
			headers["X-Photo-Reconcile"] = "1"
		}
		return a.At(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, headers)
	}
	names, images := []string{"front.jpg", "front.jpg"}, [][]byte{jpg, jpg}
	if r := send(names, images, true); r.Status != 409 {
		t.Fatalf("absent batch: %d", r.Status)
	}
	if r := send(names, images, false); r.Status != 200 {
		t.Fatalf("save: %d %s", r.Status, r.Body)
	}
	for i := 0; i < 2; i++ {
		r := send(names, images, true)
		if r.Status != 200 || r.JSON(t)["version"] != float64(2) || r.JSON(t)["reconciled"] != true {
			t.Fatalf("reconcile: %d %s", r.Status, r.Body)
		}
		photos := r.JSON(t)["photos"].([]any)
		if photos[0].(map[string]any)["path"] == photos[1].(map[string]any)["path"] {
			t.Fatal("same file counted twice")
		}
	}
	// Exact names take precedence over another photo's collision suffix.
	if r := send([]string{"front.jpg", "front-2.jpg"}, images, true); r.Status != 200 {
		t.Fatalf("overlapping names: %d %s", r.Status, r.Body)
	}
	for _, tc := range []struct {
		names  []string
		images [][]byte
	}{
		{[]string{"front.jpg", "front.jpg", "front.jpg"}, [][]byte{jpg, jpg, jpg}},
		{[]string{"front.jpg"}, [][]byte{photoJPEG(t, 9, 6)}},
		{[]string{"other.jpg"}, [][]byte{jpg}},
	} {
		if r := send(tc.names, tc.images, true); r.Status != 409 {
			t.Fatalf("unconfirmed: %d %s", r.Status, r.Body)
		}
	}
	versions, err := sitesrepo.ListVersionsBySite(context.Background(), a.Database, a.SiteID(t, p, "home"))
	if err != nil || len(versions) != 2 {
		t.Fatalf("reconciliation wrote a version: %d %v", len(versions), err)
	}
}
