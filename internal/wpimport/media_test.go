package wpimport

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"sync/atomic"
	"testing"
)

func TestURLRewriting(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{{"https://example.org/wp-content/uploads/2026/07/photo-300x200.png?w=300", "../../wp-content/uploads/2026/07/photo.png"}, {"http://www.example.org/wp-content/uploads/2026/07/photo.png", "../../wp-content/uploads/2026/07/photo.png"}, {"https://family.files.wordpress.com/2026/07/photo.png?resize=30,20", "../../wp-content/uploads/2026/07/photo.png"}, {"https://i0.wp.com/example.org/wp-content/uploads/2026/07/photo.png?ssl=1", "../../wp-content/uploads/2026/07/photo.png"}, {"https://example.org/about/#team", "../../about/#team"}, {"https://example.net/photo.jpg", "https://example.net/photo.jpg"}, {"#section", "#section"}, {"/search/?q=test", "../../search/?q=test"}} {
		t.Run(tt.raw, func(t *testing.T) {
			c := testConverter()
			c.result.Files["wp-content/uploads/2026/07/photo.png"] = []byte{1}
			c.published = []*item{{Path: "/about/"}}
			if got := c.rewriteURL(tt.raw, "/parent/child/"); got != tt.want {
				t.Fatalf("%q want %q", got, tt.want)
			}
		})
	}
	c := testConverter()
	c.result.Files["wp-content/uploads/2026/07/photo.png"] = []byte{1}
	got := c.cleanContent(`<img src="https://example.org/wp-content/uploads/2026/07/photo-300x200.png" srcset="a 2x" sizes="10vw">`, "/post/")
	if strings.Contains(got, "srcset") || strings.Contains(got, "sizes") {
		t.Error(got)
	}
}
func TestArchiveMatching(t *testing.T) {
	for _, tt := range []struct {
		name, target, want string
		keys               []string
	}{{"suffix", "wp-content/uploads/2026/07/photo.jpg", "backup/2026/07/photo.jpg", []string{"backup/2026/07/photo.jpg", "2025/01/photo.jpg"}}, {"basename", "2026/07/photo.jpg", "photo.jpg", []string{"photo.jpg", "other.png"}}, {"ambiguous", "2026/07/photo.jpg", "", []string{"x/photo.jpg", "y/photo.jpg"}}, {"absent", "a.jpg", "", []string{"b.jpg"}}} {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]MediaFile{}
			for _, k := range tt.keys {
				m[k] = MediaFile{}
			}
			if got := archiveMatch(tt.target, m); got != tt.want {
				t.Error(got, tt.want)
			}
		})
	}
}
func TestImageShrinker(t *testing.T) {
	for _, tt := range []struct {
		format     string
		alpha      bool
		pixelLimit int64
		wantEdge   int
		skip       bool
	}{{"png", true, 1_000_000, 100, false}, {"jpeg", false, 1_000_000, 100, false}, {"gif", false, 1_000_000, 400, false}, {"png", true, 1, 400, false}} {
		t.Run(tt.format+jsonNumber(float64(tt.pixelLimit)), func(t *testing.T) {
			im := image.NewNRGBA(image.Rect(0, 0, 400, 200))
			for y := 0; y < 200; y++ {
				for x := 0; x < 400; x++ {
					a := uint8(255)
					if tt.alpha {
						a = uint8(x % 255)
					}
					im.Set(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x * y), a})
				}
			}
			var buf bytes.Buffer
			switch tt.format {
			case "png":
				_ = png.Encode(&buf, im)
			case "jpeg":
				_ = jpeg.Encode(&buf, im, &jpeg.Options{Quality: 95})
			case "gif":
				_ = gif.Encode(&buf, im, nil)
			}
			l := DefaultLimits
			l.ImageMaxEdge = 100
			l.MaxImagePixels = tt.pixelLimit
			b, _ := shrinkImage(buf.Bytes(), l)
			cfg, format, e := image.DecodeConfig(bytes.NewReader(b))
			if e != nil || cfg.Width != tt.wantEdge {
				t.Fatal(cfg, e)
			}
			if tt.alpha && format != "png" {
				t.Error("alpha image converted to lossy format")
			}
			if len(b) > buf.Len() {
				t.Error("shrinker grew file")
			}
		})
	}
	b := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	if out, _ := shrinkImage(b, DefaultLimits); !bytes.Equal(out, b) {
		t.Error("SVG changed")
	}
	l := DefaultLimits
	l.MaxImagePixels = 1
	big := append(append([]byte(nil), testPNG(t)...), make([]byte, 2<<20)...)
	if out, _ := shrinkImage(big, l); out != nil {
		t.Error("huge pixel image decoded")
	}
}
func testPNG(t *testing.T) []byte {
	t.Helper()
	m := testMedia()["2026/07/photo.png"]
	r, e := m.Open()
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	return b
}
func TestBudget(t *testing.T) {
	for _, tt := range []struct {
		name       string
		limit      int64
		files      map[string][]byte
		keep, drop string
	}{{"nonimage first", 1000, map[string][]byte{"index.html": []byte("small"), "wp-content/uploads/a.pdf": make([]byte, 2000), "wp-content/uploads/photo.png": testPNG(t)}, "wp-content/uploads/photo.png", "wp-content/uploads/a.pdf"}, {"image skipped", 20, map[string][]byte{"index.html": []byte("small"), "wp-content/uploads/photo.png": testPNG(t)}, "index.html", "wp-content/uploads/photo.png"}, {"text alone", 4, map[string][]byte{"index.html": []byte("large document")}, "", "index.html"}} {
		t.Run(tt.name, func(t *testing.T) {
			c := testConverter()
			c.limits.MaxSiteBytes = tt.limit
			c.result.Files = tt.files
			c.result.Report.MediaCopied = 2
			for p := range tt.files {
				if strings.HasPrefix(p, "wp-content/uploads/") {
					c.mediaOriginal[p] = "https://example.org/" + p
				}
			}
			c.fitBudget()
			if c.result.Report.TotalBytes > tt.limit {
				t.Error("over budget")
			}
			if tt.keep != "" && c.result.Files[tt.keep] == nil {
				t.Error("removed", tt.keep)
			}
			if _, ok := c.result.Files[tt.drop]; ok {
				t.Error("retained", tt.drop)
			}
			if len(c.result.Report.Warnings) == 0 {
				t.Error("no budget warning")
			}
		})
	}
}
func TestFetchFailuresAndConcurrency(t *testing.T) {
	c := testConverter()
	for n := 0; n < 8; n++ {
		c.data.Items = append(c.data.Items, &item{Attachment: "https://example.org/wp-content/uploads/2026/07/" + jsonNumber(float64(n)) + ".png"})
	}
	var active, peak atomic.Int32
	c.in.Fetch = func(context.Context, string) ([]byte, error) {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer active.Add(-1)
		return nil, errors.New("offline")
	}
	c.limits.FetchConcurrency = 2
	c.copyMedia()
	if len(c.result.Report.MediaMissing) != 8 || peak.Load() > 2 {
		t.Error(c.result.Report, peak.Load())
	}
}

func TestArchiveCanonicalCopy(t *testing.T) {
	c := testConverter()
	c.in.Media = testMedia()
	m := c.in.Media["2026/07/photo.png"]
	c.in.Media = map[string]MediaFile{"backup/2026/07/photo.png": m}
	c.data.Items = []*item{{Attachment: "https://example.org/wp-content/uploads/2026/07/photo.png", Content: `<img src="https://i2.wp.com/example.org/wp-content/uploads/2026/07/photo-300x200.png?w=300">`}}
	c.copyMedia()
	if c.result.Report.MediaCopied != 1 || len(c.result.Files) != 1 || c.result.Files["wp-content/uploads/2026/07/photo.png"] == nil {
		t.Fatal(c.result.Report, sortedKeys(c.result.Files))
	}
}
func TestMediaBudgetDuringCopy(t *testing.T) {
	c := testConverter()
	c.limits.MaxSiteBytes = 100
	c.in.Media = map[string]MediaFile{}
	for _, name := range []string{"a.pdf", "b.pdf", "c.pdf"} {
		b := bytes.Repeat([]byte(name[:1]), 60)
		c.in.Media[name] = MediaFile{Size: 60, Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(b)), nil }}
	}
	c.copyMedia()
	if c.mediaBytes() > 100 || c.result.Report.MediaCopied != 1 {
		t.Error(c.mediaBytes(), c.result.Report)
	}
	if c.result.Files["wp-content/uploads/c.pdf"] == nil {
		t.Error("deterministic size ordering changed")
	}
}
