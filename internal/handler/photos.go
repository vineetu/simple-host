package handler

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"golang.org/x/image/webp"
)

const photoCountLimit = 30

var photoName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}\.(webp|jpg)$`)

func photoLimit(key string, fallback int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func photosHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hackMode {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type photoResult struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	Bytes  int    `json:"bytes"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

func (h *SiteHandler) addPhotos(w http.ResponseWriter, r *http.Request) {
	// Dashboard authentication is an explicit X-API-Key, never a visitor cookie.
	// Reuse the account sign-in origin policy for this apex browser action.
	if base := publicBaseOrigin(h.publicBaseURL); base != nil && !accountAuthAllowed(r, base) {
		writeOriginRefused(w)
		return
	}
	user := auth.GetUser(r.Context())
	name := r.PathValue("sitename")
	if err := validateSiteShape(name); err != nil {
		writeJSON(w, 400, errorResponse{Error: "Invalid site name."})
		return
	}
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeDeploySiteNotFound(w, r, name)
		} else {
			writeJSON(w, 500, errorResponse{Error: "Could not read this site."})
		}
		return
	}
	cap := int64(photoLimit("PHOTO_MAX_BYTES", 1000000))
	bodyCap := photoCountLimit*(cap+4096) + 4096
	if r.ContentLength > bodyCap {
		writeJSON(w, 413, errorResponse{Error: "The photo upload is too large."})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, bodyCap)
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, 400, errorResponse{Error: "Choose photos to upload."})
		return
	}
	type upload struct {
		name   string
		data   []byte
		config image.Config
	}
	uploads := []upload{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeJSON(w, 400, errorResponse{Error: "The photo upload is incomplete or too large."})
			return
		}
		if len(uploads) == photoCountLimit {
			writeJSON(w, 400, errorResponse{Error: "Choose up to 30 photos at a time."})
			return
		}
		// Part.FileName applies filepath.Base: inspect the original parameter instead.
		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		filename := params["filename"]
		if err != nil || part.FormName() != "photos" || !photoName.MatchString(filename) {
			writeJSON(w, 400, errorResponse{Error: "Use a photo name with lowercase letters, numbers, and hyphens, followed by .webp or .jpg."})
			return
		}
		data, err := io.ReadAll(io.LimitReader(part, cap+1))
		if int64(len(data)) > cap {
			writeJSON(w, 413, errorResponse{Error: "A photo is too large. Choose it again to make a smaller copy."})
			return
		}
		if err != nil {
			writeJSON(w, 400, errorResponse{Error: "The photo upload is incomplete."})
			return
		}
		cfg, ext, err := validatePhoto(data, photoLimit("PHOTO_MAX_DIMENSION", 2048))
		if err != nil {
			writeJSON(w, 400, errorResponse{Error: err.Error()})
			return
		}
		if !strings.HasSuffix(filename, "."+ext) {
			writeJSON(w, 400, errorResponse{Error: "The photo extension does not match its contents."})
			return
		}
		uploads = append(uploads, upload{filename, data, cfg})
	}
	if len(uploads) == 0 {
		writeJSON(w, 400, errorResponse{Error: "Choose at least one photo."})
		return
	}
	unlockAccount := h.lockAccountFiles(user.ID)
	defer unlockAccount()
	unlockSite := h.lockSite(user.ID, name)
	defer unlockSite()
	limit, override := siteLimitFor(r.Context(), h.database, user)
	// Load under the deploy locks so simultaneous deploys cannot lose files.
	files, digest, err := h.finishJSONFiles(w, r, name, map[string][]byte{}, nil, limit, override, true)
	if err != nil {
		return
	}
	results := make([]photoResult, 0, len(uploads))
	for _, p := range uploads {
		ext := p.name[strings.LastIndexByte(p.name, '.'):]
		stem := strings.TrimSuffix(p.name, ext)
		path := "images/" + p.name
		for n := 2; photoPathTaken(files, path); n++ {
			suffix := fmt.Sprintf("-%d", n)
			base := stem
			if len(base)+len(suffix) > 60 {
				base = base[:60-len(suffix)]
			}
			path = "images/" + base + suffix + ext
		}
		files[path] = p.data
		results = append(results, photoResult{Path: path, Bytes: len(p.data), Width: p.config.Width, Height: p.config.Height})
	}
	files, digest, err = h.finishJSONFiles(w, r, name, files, nil, limit, override, false)
	if err != nil {
		return
	}
	h.commitSiteUpdateRespond(w, r, user, name, files, digest, true, func(site siteResponse) {
		for i := range results {
			results[i].URL = strings.TrimRight(site.SiteURL, "/") + "/" + results[i].Path
		}
		writeJSON(w, 200, struct {
			Site    string        `json:"site"`
			Version int           `json:"version"`
			Photos  []photoResult `json:"photos"`
		}{name, site.ActiveVersion, results})
	})
}

func photoPathTaken(files map[string][]byte, path string) bool {
	for existing := range files {
		if existing == path || strings.HasPrefix(existing, path+"/") {
			return true
		}
	}
	return false
}

var errPhotoContainer = errors.New("This photo is incomplete or is not a supported WebP or JPEG image.")
var errPhotoMetadata = errors.New("This photo still contains camera or location metadata. Choose it again to make a clean copy.")

func validatePhoto(data []byte, maxDimension int) (image.Config, string, error) {
	var cfg image.Config
	var err error
	ext := ""
	switch {
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		ext = "webp"
		if err = checkPhotoWebP(data); err == nil {
			cfg, err = webp.DecodeConfig(bytes.NewReader(data))
		}
	case len(data) >= 3 && bytes.Equal(data[:3], []byte{255, 216, 255}):
		ext = "jpg"
		if err = checkPhotoJPEG(data); err == nil {
			cfg, err = jpeg.DecodeConfig(bytes.NewReader(data))
		}
	default:
		err = errPhotoContainer
	}
	if err != nil {
		if errors.Is(err, errPhotoMetadata) {
			return cfg, ext, err
		}
		return cfg, ext, errPhotoContainer
	}
	if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > maxDimension || cfg.Height > maxDimension {
		return cfg, ext, fmt.Errorf("Photos must be no more than %d pixels on their longest side.", maxDimension)
	}
	return cfg, ext, nil
}

// Walk all RIFF chunks, not just the first header read by DecodeConfig.
func checkPhotoWebP(b []byte) error {
	if uint64(binary.LittleEndian.Uint32(b[4:8]))+8 != uint64(len(b)) {
		return errPhotoContainer
	}
	imageSeen, extended, alpha, icc := false, false, false, false
	var canvasWidth, canvasHeight int
	var flags byte
	for off := 12; off < len(b); {
		if len(b)-off < 8 {
			return errPhotoContainer
		}
		kind := string(b[off : off+4])
		n := uint64(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		off += 8
		if n > uint64(len(b)-off) {
			return errPhotoContainer
		}
		chunk := b[off : off+int(n)]
		switch kind {
		case "EXIF", "XMP ":
			return errPhotoMetadata
		case "VP8X":
			if off != 20 || extended || n != 10 {
				return errPhotoContainer
			}
			extended = true
			flags = chunk[0]
			canvasWidth = 1 + int(chunk[4]) + int(chunk[5])<<8 + int(chunk[6])<<16
			canvasHeight = 1 + int(chunk[7]) + int(chunk[8])<<8 + int(chunk[9])<<16
			if chunk[0]&0x0c != 0 {
				return errPhotoMetadata
			}
			if chunk[0]&0xc3 != 0 || chunk[1] != 0 || chunk[2] != 0 || chunk[3] != 0 {
				return errPhotoContainer
			}
		case "VP8 ", "VP8L":
			if imageSeen || n == 0 {
				return errPhotoContainer
			}
			imageSeen = true
			// A VP8 keyframe's first partition must fit inside the chunk.
			if kind == "VP8 " && (n < 10 || chunk[0]&1 != 0 || int(chunk[0]>>5)|int(chunk[1])<<3|int(chunk[2])<<11 > len(chunk)-10) {
				return errPhotoContainer
			}
			if kind == "VP8L" && (n < 5 || chunk[0] != 0x2f || alpha) {
				return errPhotoContainer
			}
			// DecodeConfig stops at VP8X in x/image. Also parse the actual
			// image header so a small canvas cannot hide a larger payload.
			header := make([]byte, 20)
			copy(header, "RIFF")
			binary.LittleEndian.PutUint32(header[4:8], uint32(n)+12)
			copy(header[8:], "WEBP"+kind)
			binary.LittleEndian.PutUint32(header[16:20], uint32(n))
			cfg, err := webp.DecodeConfig(io.MultiReader(bytes.NewReader(header), bytes.NewReader(chunk)))
			if err != nil || extended && (cfg.Width != canvasWidth || cfg.Height != canvasHeight) {
				return errPhotoContainer
			}
			if extended && ((flags&0x10 != 0) != (alpha || kind == "VP8L" && chunk[4]&0x10 != 0)) {
				return errPhotoContainer
			}
		case "ALPH":
			if !extended || imageSeen || alpha || n == 0 || chunk[0]&0xc3 > 1 {
				return errPhotoContainer
			}
			alpha = true
			if chunk[0]&3 == 0 && n != uint64(1+canvasWidth*canvasHeight) {
				return errPhotoContainer
			}
		case "ICCP":
			if !extended || imageSeen || alpha || icc || n == 0 {
				return errPhotoContainer
			}
			icc = true
		default:
			return errPhotoContainer // Includes animation chunks and extra payloads.
		}
		off += int(n)
		if n&1 != 0 {
			if off >= len(b) || b[off] != 0 {
				return errPhotoContainer
			}
			off++
		}
	}
	if !imageSeen || extended && (flags&0x20 != 0) != icc {
		return errPhotoContainer
	}
	return nil
}

// Validate the marker stream through EOI, including entropy byte stuffing and
// scan boundaries. DecodeConfig then validates the image header. No pixel decode.
func checkPhotoJPEG(b []byte) error {
	scans := 0
	frameSeen := false
	for off := 2; off < len(b); {
		if b[off] != 255 {
			return errPhotoContainer
		}
		for off < len(b) && b[off] == 255 {
			off++
		}
		if off >= len(b) {
			return errPhotoContainer
		}
		marker := b[off]
		off++
		if marker == 0xd9 {
			if off == len(b) && scans > 0 {
				return nil
			}
			return errPhotoContainer
		}
		if off+2 > len(b) {
			return errPhotoContainer
		}
		n := int(binary.BigEndian.Uint16(b[off : off+2]))
		if n < 2 || n > len(b)-off {
			return errPhotoContainer
		}
		payload := b[off+2 : off+n]
		switch marker {
		case 0xe1:
			return errPhotoMetadata // All APP1, including Exif and both XMP forms.
		case 0xe0:
			if len(payload) < 14 || string(payload[:5]) != "JFIF\x00" || len(payload) != 14+3*int(payload[12])*int(payload[13]) {
				return errPhotoContainer
			}
		case 0xe2:
			if len(payload) < 14 || string(payload[:12]) != "ICC_PROFILE\x00" {
				return errPhotoContainer
			}
		case 0xc0, 0xc1, 0xc2:
			if frameSeen || scans != 0 {
				return errPhotoContainer
			}
			frameSeen = true
		case 0xda:
			if !frameSeen || len(payload) < 6 || payload[0] < 1 || payload[0] > 4 || len(payload) != 4+2*int(payload[0]) {
				return errPhotoContainer
			}
		case 0xc4, 0xdb, 0xdd:
		default:
			return errPhotoContainer // No comments or arbitrary application data.
		}
		off += n
		if marker == 0xda {
			scans++
			start := off
			for off < len(b) {
				if b[off] != 255 {
					off++
					continue
				}
				if off+1 >= len(b) {
					return errPhotoContainer
				}
				if b[off+1] == 0 || b[off+1] >= 0xd0 && b[off+1] <= 0xd7 {
					off += 2
					continue
				}
				break
			}
			if off == start {
				return errPhotoContainer
			}
		}
	}
	return errPhotoContainer
}
