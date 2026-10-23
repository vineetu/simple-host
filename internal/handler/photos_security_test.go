package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhotoCollisionIndex(t *testing.T) {
	files := map[string][]byte{"images/front.jpg": nil, "images/dir.jpg/child": nil}
	for i := 2; i <= 20000; i++ {
		files[fmt.Sprintf("images/front-%d.jpg", i)] = nil
	}
	occupied, err := photoOccupiedPaths(context.Background(), files)
	if err != nil {
		t.Fatal(err)
	}
	path, err := nextPhotoPath(context.Background(), occupied, "front.jpg")
	if err != nil || path != "images/front-20001.jpg" || !occupied["images/dir.jpg"] {
		t.Fatalf("collision: %s %v", path, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := photoOccupiedPaths(ctx, files); !errors.Is(err, context.Canceled) {
		t.Fatal("index ignored cancellation")
	}
	if _, err := nextPhotoPath(ctx, occupied, "front.jpg"); !errors.Is(err, context.Canceled) {
		t.Fatal("search ignored cancellation")
	}
}

type unreadPhotoBody struct{ t *testing.T }

func (b unreadPhotoBody) Read([]byte) (int, error) {
	b.t.Fatal("busy request read its body")
	return 0, nil
}
func (b unreadPhotoBody) Close() error { return nil }

func TestPhotoSaveConcurrency(t *testing.T) {
	for _, limit := range []int{3, 1} {
		t.Setenv("PHOTO_MAX_CONCURRENT", fmt.Sprint(limit))
		for i := 0; i < limit; i++ {
			if !acquirePhotoSave() {
				t.Fatal("slot refused")
			}
		}
		func() {
			defer photoSaves.Add(-int64(limit))
			// No DB or auth context: overload must stop before either and before reading.
			h := &SiteHandler{}
			req := httptest.NewRequest("POST", "/v1/sites/home/photos", unreadPhotoBody{t})
			w := httptest.NewRecorder()
			h.addPhotos(w, req)
			if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), "Busy, try again in a moment") {
				t.Fatalf("busy response: %d", w.Code)
			}
		}()
		if !acquirePhotoSave() {
			t.Fatal("slot not released")
		}
		photoSaves.Add(-1)
	}
}

func TestPhotoAncestorConflicts(t *testing.T) {
	for _, path := range []string{"images", "images/front.jpg/child"} {
		t.Run(path, func(t *testing.T) {
			a := newPersonApp(t, "off")
			p := a.newPerson(t, "photoconflict")
			a.deploy(t, p, "home")
			uid, _ := a.userID(t, p)
			dest := filepath.Join(a.sites.disk.VersionDir(uid, "home", 1), path)
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			body, ct := photoMultipart(t, []string{"front.jpg"}, [][]byte{photoJPEG(t, 8, 6)})
			r := a.at(t, "POST", "simple-host.test", "/v1/sites/home/photos", body, map[string]string{"X-API-Key": p.key, "Content-Type": ct})
			if r.status != 409 {
				t.Fatalf("conflict: %d %s", r.status, r.body)
			}
			if got, err := os.ReadFile(dest); err != nil || string(got) != "keep" {
				t.Fatal("conflicting file changed")
			}
		})
	}
}

func TestPhotoReservedNamesKeepExistingHosts(t *testing.T) {
	for _, handle := range []string{"photos", "photo-codecs"} {
		if !labelReservedForNew(handle) || !handleAddressable(handle) {
			t.Fatalf("claim/serve policy: %s", handle)
		}
		t.Run(handle, func(t *testing.T) {
			a, dir := newSiteApp(t, "serve")
			a.sites.SetPersonHosts("serve")
			markReady(t, dir, handle)
			p := a.newPerson(t, "oldphoto")
			uid, _ := a.userID(t, p)
			if _, err := a.database.Exec(`UPDATE users SET handle=$1 WHERE id=$2`, handle, uid); err != nil {
				t.Fatal(err)
			}
			a.deploy(t, p, "home")
			for host, path := range map[string]string{handle + "." + pcSiteDomain: "/home/", "home." + handle + "." + pcSiteDomain: "/"} {
				r := a.at(t, "GET", host, path, nil, nil)
				if r.status != 200 || string(r.body) != "<h1>home</h1>" {
					t.Fatalf("existing host %s: %d", host, r.status)
				}
			}
		})
	}
}

func photoAPP(b []byte, marker byte, payload []byte) []byte {
	out := append([]byte{}, b[:2]...)
	out = append(out, 255, marker)
	out = binary.BigEndian.AppendUint16(out, uint16(len(payload)+2))
	out = append(out, payload...)
	return append(out, b[2:]...)
}

func TestPhotoMetadataAllowlist(t *testing.T) {
	jpg := photoJPEG(t, 8, 6)
	icc := append([]byte("ICC_PROFILE\x00\x01\x01"), make([]byte, 16384-14)...)
	jfif := append([]byte("JFIF\x00"), make([]byte, 9)...)
	adobe := append([]byte("Adobe"), make([]byte, 7)...)
	for _, b := range [][]byte{photoAPP(jpg, 0xe0, jfif), photoAPP(jpg, 0xe2, icc), photoAPP(jpg, 0xee, adobe)} {
		if _, _, err := validatePhoto(b, 2048); err != nil {
			t.Fatal(err)
		}
	}
	for marker := byte(0xe0); marker <= 0xef; marker++ {
		if marker == 0xe0 || marker == 0xe2 || marker == 0xee {
			continue
		}
		if _, _, err := validatePhoto(photoAPP(jpg, marker, []byte("private")), 2048); err == nil {
			t.Fatalf("APP%x accepted", marker)
		}
	}
	for _, b := range [][]byte{photoAPP(jpg, 0xfe, []byte("comment")), photoAPP(photoAPP(jpg, 0xe2, icc), 0xe2, icc), photoAPP(jpg, 0xee, []byte("private")), append(bytes.Clone(jpg), 'x')} {
		if _, _, err := validatePhoto(b, 2048); err == nil {
			t.Fatal("JPEG metadata accepted")
		}
	}
	base := photoWebP(t, "alpha")
	for _, size := range []int{16384, 16385} {
		b := photoChunk(base[:30], "ICCP", make([]byte, size))
		b[20] |= 0x20
		b = append(b, base[30:]...)
		binary.LittleEndian.PutUint32(b[4:8], uint32(len(b)-8))
		_, _, err := validatePhoto(b, 2048)
		if (err == nil) != (size == 16384) {
			t.Fatalf("WebP ICC size %d: %v", size, err)
		}
	}
	for _, kind := range []string{"EXIF", "XMP ", "JUNK"} {
		if _, _, err := validatePhoto(photoChunk(base, kind, []byte("private")), 2048); err == nil {
			t.Fatalf("WebP %s accepted", kind)
		}
	}
}

func TestPhotoCanvasFixtures(t *testing.T) {
	paths, err := filepath.Glob("testdata/photos/canvas-*.*")
	if err != nil || len(paths) == 0 {
		t.Fatal("missing canvas fixtures")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := validatePhoto(b, 2048); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPhotoDecoderSafety(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	cmd := exec.Command("node", "../../scripts/test-photo-decoders.cjs")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("decoder tests: %v\n%s", err, out)
	}
}

func TestPhotoStaticServingNoSniff(t *testing.T) {
	for _, file := range []string{"../../deploy/compose/Caddyfile", "../../deploy/platforms/coolify/Caddyfile", "../../deploy/platforms/fly/Caddyfile", "../../deploy/platforms/render/Caddyfile", "../../deploy/Caddyfile.v3-content-host.example"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		// Each direct file_server must set the header in the same route.
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "file_server" && (i == 0 || strings.TrimSpace(lines[i-1]) != "header X-Content-Type-Options nosniff") {
				t.Errorf("%s: file_server lacks nosniff", file)
			}
		}
	}
}
