package web

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"testing"
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
		"jpeg polyglot": append(append([]byte{}, jpg...), []byte("<html>hello</html>")...),
		"jpeg exif":     photoAPP1(jpg, "Exif\x00\x00GPS"), "jpeg xmp": photoAPP1(jpg, "http://ns.adobe.com/xap/1.0/\x00GPS"),
		"webp exif": photoChunk(webp, "EXIF", []byte("GPS")), "webp xmp": photoChunk(webp, "XMP ", []byte("GPS")),
		"webp trailing": append(append([]byte{}, webp...), []byte("<html/>")...),
		"dimensions":    photoJPEG(t, 2049, 1),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := validatePhoto(b, 2048); err == nil {
				t.Fatal("accepted")
			}
		})
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
