package web

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	// The container and size are checked, not the profile contents.
	copy(icc[14:], "arbitrary ICC contents")
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
		profile := make([]byte, size)
		copy(profile, "arbitrary ICC contents")
		b := photoChunk(base[:30], "ICCP", profile)
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
	for _, file := range []string{"../../deploy/compose/Caddyfile", "../../deploy/Caddyfile.v3-content-host.example"} {
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
