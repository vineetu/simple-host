package imagescan

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	"golang.org/x/image/draw"

	// Decoders register themselves with package image on import.
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "image/gif"
)

// Defaults for PrepareOptions.
const (
	DefaultMaxBytes int64 = 4 << 20
	DefaultMinSide        = 50
	DefaultMaxSide        = 1600
	// maxPixels bounds decoding cost. DecodeConfig is cheap; a full decode
	// of more pixels than this is refused before it starts.
	maxPixels = 50_000_000
)

// Skip reasons Prepare returns. The worker records them as VerdictSkipped.
const (
	SkipEmpty         = "empty file"
	SkipNotRaster     = "not a raster image"
	SkipTooSmall      = "too small"
	SkipTooManyPixels = "too many pixels"
	SkipUndecodable   = "undecodable"
	SkipTooLarge      = "too large after resize"
)

// Prepare sniffs raw upload bytes and returns an Image ready to send, or a
// non-empty skip reason when the bytes are not an image the service can
// check (SVG, ICO, a text file with an image name, a 16 px favicon, a
// decode failure). err is for genuine failures only (an encoder error).
// WebP is re-encoded as PNG; oversized images are scaled to MaxSide.
//
// SHA256 on the result is always the hex digest of raw, even after a
// re-encode. ContentType, Width, and Height describe the bytes in Data.
func Prepare(raw []byte, opts PrepareOptions) (img *Image, skip string, err error) {
	opts = opts.withDefaults()
	if len(raw) == 0 {
		return nil, SkipEmpty, nil
	}
	sum := sha256.Sum256(raw)
	sha := hex.EncodeToString(sum[:])

	cfg, format, derr := safeDecodeConfig(raw)
	if derr != nil {
		return nil, notRasterReason(raw), nil
	}
	if !acceptedFormat(format) {
		return nil, SkipNotRaster + " (" + format + ")", nil
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, SkipUndecodable, nil
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, SkipTooManyPixels, nil
	}
	if cfg.Width < opts.MinSide || cfg.Height < opts.MinSide {
		return nil, SkipTooSmall, nil
	}

	// Every candidate is decoded in full, so a truncated or corrupt file
	// is caught here rather than by the service. The pixel guard above
	// bounds the cost.
	m, derr := safeDecode(raw)
	if derr != nil {
		return nil, SkipUndecodable, nil
	}

	tooBig := int64(len(raw)) > opts.MaxBytes
	// JPEG, PNG, BMP, and TIFF go as uploaded when they fit. GIF (first
	// frame) and WebP are always re-encoded as PNG.
	if !tooBig && format != "gif" && format != "webp" {
		return &Image{Data: raw, ContentType: "image/" + format, Width: cfg.Width, Height: cfg.Height, SHA256: sha}, "", nil
	}

	var out []byte
	var ct string
	scaled := false
	if tooBig {
		m = downscale(m, opts.MaxSide)
		scaled = true
		out, ct, err = encodeJPEG(m)
	} else {
		out, ct, err = encodePNG(m)
	}
	if err != nil {
		return nil, "", err
	}
	if int64(len(out)) > opts.MaxBytes && !scaled {
		m = downscale(m, opts.MaxSide)
		out, ct, err = encodeJPEG(m)
		if err != nil {
			return nil, "", err
		}
	}
	if int64(len(out)) > opts.MaxBytes {
		return nil, SkipTooLarge, nil
	}
	b := m.Bounds()
	return &Image{Data: out, ContentType: ct, Width: b.Dx(), Height: b.Dy(), SHA256: sha}, "", nil
}

func (o PrepareOptions) withDefaults() PrepareOptions {
	if o.MaxBytes <= 0 {
		o.MaxBytes = DefaultMaxBytes
	}
	if o.MinSide <= 0 {
		o.MinSide = DefaultMinSide
	}
	if o.MaxSide <= 0 {
		o.MaxSide = DefaultMaxSide
	}
	return o
}

func acceptedFormat(format string) bool {
	switch format {
	case "jpeg", "png", "gif", "bmp", "tiff", "webp":
		return true
	}
	return false
}

// notRasterReason names why DecodeConfig failed, as far as a cheap sniff can
// tell. Bytes that sniff as a supported image but do not decode are
// "undecodable" (a truncated or corrupt file).
func notRasterReason(raw []byte) string {
	ct := http.DetectContentType(raw)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	head := raw
	if len(head) > 512 {
		head = head[:512]
	}
	lower := strings.ToLower(string(head))
	switch {
	case strings.Contains(lower, "<svg"):
		return SkipNotRaster + " (svg)"
	case ct == "image/x-icon":
		return SkipNotRaster + " (icon)"
	case isHEIF(raw):
		return SkipNotRaster + " (heic)"
	case ct == "text/html":
		return SkipNotRaster + " (html)"
	case strings.HasPrefix(ct, "text/"):
		return SkipNotRaster + " (text)"
	case ct == "image/jpeg", ct == "image/png", ct == "image/gif", ct == "image/bmp", ct == "image/webp":
		return SkipUndecodable
	}
	return SkipNotRaster
}

// isHEIF spots the ISO base media brands HEIC and AVIF use.
func isHEIF(raw []byte) bool {
	if len(raw) < 12 || string(raw[4:8]) != "ftyp" {
		return false
	}
	switch string(raw[8:12]) {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1", "avif", "avis":
		return true
	}
	return false
}

func safeDecodeConfig(raw []byte) (cfg image.Config, format string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("imagescan: decode config panic: %v", r)
		}
	}()
	return image.DecodeConfig(bytes.NewReader(raw))
}

func safeDecode(raw []byte) (m image.Image, err error) {
	defer func() {
		if r := recover(); r != nil {
			m = nil
			err = fmt.Errorf("imagescan: decode panic: %v", r)
		}
	}()
	// image.Decode hands a GIF to gif.Decode, which returns the first frame.
	m, _, err = image.Decode(bytes.NewReader(raw))
	return m, err
}

// downscale returns m scaled so its longest side is maxSide, or m itself
// when it already fits.
func downscale(m image.Image, maxSide int) image.Image {
	b := m.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxSide && h <= maxSide {
		return m
	}
	nw, nh := maxSide, maxSide
	if w >= h {
		nh = h * maxSide / w
	} else {
		nw = w * maxSide / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), m, b, draw.Src, nil)
	return dst
}

func encodeJPEG(m image.Image) ([]byte, string, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: 85}); err != nil {
		return nil, "", fmt.Errorf("imagescan: encode jpeg: %w", err)
	}
	return buf.Bytes(), "image/jpeg", nil
}

func encodePNG(m image.Image) ([]byte, string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		return nil, "", fmt.Errorf("imagescan: encode png: %w", err)
	}
	return buf.Bytes(), "image/png", nil
}
