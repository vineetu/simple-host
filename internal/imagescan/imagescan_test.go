package imagescan

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"
)

// testImage is a w x h picture with enough variation that JPEG cannot
// shrink it to nothing.
func testImage(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 3), uint8((x ^ y) * 5), 255})
		}
	}
	return m
}

func noiseImage(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(1))
	r.Read(m.Pix)
	for i := 3; i < len(m.Pix); i += 4 {
		m.Pix[i] = 255
	}
	return m
}

func encode(t *testing.T, format string, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, m, &jpeg.Options{Quality: 90})
	case "png":
		err = png.Encode(&buf, m)
	case "gif":
		err = gif.Encode(&buf, m, nil)
	case "bmp":
		err = bmp.Encode(&buf, m)
	case "tiff":
		err = tiff.Encode(&buf, m, nil)
	default:
		t.Fatalf("no encoder for %s", format)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buf.Bytes()
}

// bitWriter packs bits least significant first, as VP8L does.
type bitWriter struct {
	out  []byte
	acc  uint64
	nacc uint
}

func (b *bitWriter) write(v uint64, n uint) {
	b.acc |= v << b.nacc
	b.nacc += n
	for b.nacc >= 8 {
		b.out = append(b.out, byte(b.acc))
		b.acc >>= 8
		b.nacc -= 8
	}
}

func (b *bitWriter) bytes() []byte {
	out := b.out
	if b.nacc > 0 {
		out = append(out, byte(b.acc))
	}
	return out
}

// solidWebP builds a lossless (VP8L) WebP of one colour. Each of the five
// prefix codes has a single symbol, so the pixels cost no bits at all.
func solidWebP(w, h int, r, g, b uint8) []byte {
	bw := &bitWriter{}
	bw.write(0x2f, 8)
	bw.write(uint64(w-1), 14)
	bw.write(uint64(h-1), 14)
	bw.write(0, 1) // alpha hint
	bw.write(0, 3) // version
	bw.write(0, 1) // no transform
	bw.write(0, 1) // no colour cache
	bw.write(0, 1) // no meta prefix codes
	single := func(sym uint8) {
		bw.write(1, 1) // simple code
		bw.write(0, 1) // one symbol
		bw.write(1, 1) // eight-bit symbol
		bw.write(uint64(sym), 8)
	}
	single(g)      // green
	single(r)      // red
	single(b)      // blue
	single(255)    // alpha
	bw.write(1, 1) // distance: simple code
	bw.write(0, 1) // one symbol
	bw.write(0, 1) // one-bit symbol
	bw.write(0, 1) // symbol 0
	chunk := append(bw.bytes(), 0, 0)
	if len(chunk)%2 == 1 {
		chunk = append(chunk, 0)
	}
	var out bytes.Buffer
	out.WriteString("RIFF")
	binary.Write(&out, binary.LittleEndian, uint32(4+8+len(chunk)))
	out.WriteString("WEBP")
	out.WriteString("VP8L")
	binary.Write(&out, binary.LittleEndian, uint32(len(chunk)))
	out.Write(chunk)
	return out.Bytes()
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestSolidWebPFixtureDecodes(t *testing.T) {
	raw := solidWebP(60, 60, 10, 20, 30)
	cfg, err := webp.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("DecodeConfig: %v", err)
	}
	if cfg.Width != 60 || cfg.Height != 60 {
		t.Fatalf("size %dx%d", cfg.Width, cfg.Height)
	}
	m, err := webp.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	r, g, b, a := m.At(59, 59).RGBA()
	if r>>8 != 10 || g>>8 != 20 || b>>8 != 30 || a>>8 != 255 {
		t.Fatalf("pixel = %d %d %d %d", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestPrepareRasterFormats(t *testing.T) {
	src := testImage(200, 200)
	cases := []struct {
		format string
		raw    []byte
		wantCT string
		asIs   bool
	}{
		{"jpeg", encode(t, "jpeg", src), "image/jpeg", true},
		{"png", encode(t, "png", src), "image/png", true},
		{"gif", encode(t, "gif", src), "image/png", false},
		{"bmp", encode(t, "bmp", src), "image/bmp", true},
		{"tiff", encode(t, "tiff", src), "image/tiff", true},
		{"webp", solidWebP(200, 200, 90, 90, 90), "image/png", false},
	}
	for _, c := range cases {
		t.Run(c.format, func(t *testing.T) {
			img, skip, err := Prepare(c.raw, PrepareOptions{})
			if err != nil || skip != "" {
				t.Fatalf("Prepare: skip=%q err=%v", skip, err)
			}
			if img.ContentType != c.wantCT {
				t.Errorf("ContentType = %q, want %q", img.ContentType, c.wantCT)
			}
			if img.Width != 200 || img.Height != 200 {
				t.Errorf("size = %dx%d", img.Width, img.Height)
			}
			if img.SHA256 != shaHex(c.raw) {
				t.Errorf("SHA256 is not of the original bytes")
			}
			if c.asIs && !bytes.Equal(img.Data, c.raw) {
				t.Errorf("bytes were changed but should go as is")
			}
			if !c.asIs {
				if bytes.Equal(img.Data, c.raw) {
					t.Errorf("bytes should have been re-encoded")
				}
				if _, err := png.Decode(bytes.NewReader(img.Data)); err != nil {
					t.Errorf("re-encoded bytes are not PNG: %v", err)
				}
			}
		})
	}
}

func TestPrepareSkips(t *testing.T) {
	small := encode(t, "png", testImage(20, 20))
	jp := encode(t, "jpeg", testImage(200, 200))
	truncated := jp[:len(jp)/3]
	ico := append([]byte{0, 0, 1, 0, 1, 0, 16, 16, 0, 0, 1, 0, 32, 0}, make([]byte, 64)...)
	heic := append([]byte{0, 0, 0, 24}, []byte("ftypheic")...)
	heic = append(heic, make([]byte, 64)...)
	cases := []struct {
		name string
		raw  []byte
		want string
	}{
		{"too small", small, SkipTooSmall},
		{"svg", []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"></svg>`), SkipNotRaster + " (svg)"},
		{"svg without prolog", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), SkipNotRaster + " (svg)"},
		{"text", []byte("hello, this is a text file named photo.jpg\n"), SkipNotRaster + " (text)"},
		{"html named jpg", []byte("<!DOCTYPE html><html><body>hi</body></html>"), SkipNotRaster + " (html)"},
		{"ico", ico, SkipNotRaster + " (icon)"},
		{"heic", heic, SkipNotRaster + " (heic)"},
		{"truncated jpeg", truncated, SkipUndecodable},
		{"jpeg magic only", []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}, SkipUndecodable},
		{"empty", nil, SkipEmpty},
		{"binary junk", bytes.Repeat([]byte{0x01, 0x02, 0x80, 0xff}, 100), SkipNotRaster},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			img, skip, err := Prepare(c.raw, PrepareOptions{})
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if img != nil {
				t.Fatalf("got an image, want a skip")
			}
			if skip != c.want {
				t.Errorf("skip = %q, want %q", skip, c.want)
			}
		})
	}
}

func TestPrepareTruncatedNeverPanics(t *testing.T) {
	for _, format := range []string{"jpeg", "png", "gif", "bmp", "tiff"} {
		full := encode(t, format, testImage(120, 120))
		for _, n := range []int{8, 16, 40, len(full) / 2, len(full) - 1} {
			if n > len(full) {
				continue
			}
			if _, _, err := Prepare(full[:n], PrepareOptions{}); err != nil {
				t.Errorf("%s[:%d]: err = %v", format, n, err)
			}
		}
	}
	webpFull := solidWebP(80, 80, 1, 2, 3)
	for _, n := range []int{12, 20, 24, len(webpFull) - 2} {
		if _, _, err := Prepare(webpFull[:n], PrepareOptions{}); err != nil {
			t.Errorf("webp[:%d]: err = %v", n, err)
		}
	}
}

func TestPrepareTooManyPixels(t *testing.T) {
	// A PNG header claiming 10000 x 10000 with no real pixel data. Prepare
	// must refuse before trying to decode it.
	huge := encode(t, "png", testImage(60, 60))
	// Patch IHDR width and height (bytes 16..23) to 10000 each. The CRC no
	// longer matches, so DecodeConfig has to be tolerant for this test to
	// exercise the pixel guard; if it is not, the skip is "undecodable"
	// and the guard is covered by TestPrepareTooManyPixelsBMP instead.
	binary.BigEndian.PutUint32(huge[16:], 10000)
	binary.BigEndian.PutUint32(huge[20:], 10000)
	img, skip, err := Prepare(huge, PrepareOptions{})
	if err != nil || img != nil {
		t.Fatalf("img=%v err=%v", img, err)
	}
	if skip != SkipTooManyPixels && skip != SkipUndecodable {
		t.Errorf("skip = %q", skip)
	}
}

func TestPrepareTooManyPixelsBMP(t *testing.T) {
	// BMP headers carry no checksum, so a patched header reaches the guard.
	raw := encode(t, "bmp", testImage(60, 60))
	binary.LittleEndian.PutUint32(raw[18:], 10000) // width
	binary.LittleEndian.PutUint32(raw[22:], 10000) // height
	img, skip, err := Prepare(raw, PrepareOptions{})
	if err != nil || img != nil {
		t.Fatalf("img=%v err=%v", img, err)
	}
	if skip != SkipTooManyPixels {
		t.Errorf("skip = %q, want %q", skip, SkipTooManyPixels)
	}
}

func TestPrepareDownscalesOversizedJPEG(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a 3000x3000 JPEG")
	}
	raw := encode(t, "jpeg", noiseImage(3000, 3000))
	raw = raw[:0:0]
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, noiseImage(3000, 3000), &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	raw = buf.Bytes()
	if int64(len(raw)) <= DefaultMaxBytes {
		t.Fatalf("fixture is only %d bytes, not over %d", len(raw), DefaultMaxBytes)
	}
	img, skip, err := Prepare(raw, PrepareOptions{})
	if err != nil || skip != "" {
		t.Fatalf("Prepare: skip=%q err=%v", skip, err)
	}
	if img.ContentType != "image/jpeg" {
		t.Errorf("ContentType = %q", img.ContentType)
	}
	if img.Width != DefaultMaxSide || img.Height != DefaultMaxSide {
		t.Errorf("size = %dx%d, want %dx%d", img.Width, img.Height, DefaultMaxSide, DefaultMaxSide)
	}
	if int64(len(img.Data)) > DefaultMaxBytes {
		t.Errorf("still %d bytes", len(img.Data))
	}
	if img.SHA256 != shaHex(raw) {
		t.Errorf("SHA256 is not of the original bytes")
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil || cfg.Width != DefaultMaxSide {
		t.Errorf("sent bytes: %+v %v", cfg, err)
	}
}

func TestPrepareDownscaleKeepsAspect(t *testing.T) {
	raw := encode(t, "png", noiseImage(400, 100))
	if len(raw) <= 8000 {
		t.Fatalf("fixture is only %d bytes", len(raw))
	}
	img, skip, err := Prepare(raw, PrepareOptions{MaxBytes: 8000, MaxSide: 80})
	if err != nil || skip != "" {
		t.Fatalf("Prepare: skip=%q err=%v", skip, err)
	}
	if img.Width != 80 || img.Height != 20 {
		t.Errorf("size = %dx%d, want 80x20", img.Width, img.Height)
	}
	if img.ContentType != "image/jpeg" {
		t.Errorf("ContentType = %q", img.ContentType)
	}
	if img.SHA256 != shaHex(raw) {
		t.Errorf("SHA256 is not of the original bytes")
	}
}

func TestPrepareTooLargeAfterResize(t *testing.T) {
	raw := encode(t, "png", testImage(200, 200))
	img, skip, err := Prepare(raw, PrepareOptions{MaxBytes: 100, MaxSide: 60})
	if err != nil || img != nil {
		t.Fatalf("img=%v err=%v", img, err)
	}
	if skip != SkipTooLarge {
		t.Errorf("skip = %q, want %q", skip, SkipTooLarge)
	}
}

func TestPrepareGIFFirstFrame(t *testing.T) {
	var buf bytes.Buffer
	first := image.NewPaletted(image.Rect(0, 0, 100, 100), color.Palette{color.Black, color.White})
	second := image.NewPaletted(image.Rect(0, 0, 100, 100), color.Palette{color.Black, color.White})
	for i := range second.Pix {
		second.Pix[i] = 1
	}
	if err := gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{10, 10}}); err != nil {
		t.Fatal(err)
	}
	img, skip, err := Prepare(buf.Bytes(), PrepareOptions{})
	if err != nil || skip != "" {
		t.Fatalf("Prepare: skip=%q err=%v", skip, err)
	}
	m, err := png.Decode(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := m.At(50, 50).RGBA(); r != 0 {
		t.Errorf("sent frame is not the first (black) frame")
	}
}

func TestFakeMatcher(t *testing.T) {
	f := &Fake{Matches: map[string]Match{"abc": {IsMatch: true, Source: "NCMEC", Violations: []string{"A1"}}}}
	m, err := f.Match(nil, &Image{SHA256: "abc"})
	if err != nil || !m.IsMatch || m.Source != "NCMEC" {
		t.Fatalf("match: %+v %v", m, err)
	}
	m, err = f.Match(nil, &Image{SHA256: "def"})
	if err != nil || m.IsMatch || m.TrackingID != "fake-2" {
		t.Fatalf("clean: %+v %v", m, err)
	}
	if len(f.Calls) != 2 || f.Calls[0] != "abc" || f.Calls[1] != "def" {
		t.Errorf("Calls = %v", f.Calls)
	}
	r, err := f.Report(nil, Report{OrgName: "x"})
	if err != nil || r.ReportID != "fake-report-1" || len(f.Reports) != 1 {
		t.Errorf("report: %+v %v %d", r, err, len(f.Reports))
	}
	if f.Name() != "fake" {
		t.Errorf("Name = %q", f.Name())
	}
}
