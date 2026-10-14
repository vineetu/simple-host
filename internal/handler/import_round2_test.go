package handler

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

type bodyDeadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *bodyDeadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func checkBodyDeadline(t *testing.T, w *bodyDeadlineRecorder, start time.Time, want time.Duration) {
	t.Helper()
	if len(w.deadlines) != 2 || !w.deadlines[1].IsZero() {
		t.Fatalf("deadline was not set and cleared: %v", w.deadlines)
	}
	if got := w.deadlines[0]; got.Before(start.Add(want)) || got.After(time.Now().Add(want)) {
		t.Fatalf("deadline %v, want %v after admission", got, want)
	}
}

func TestImportReceivingBodyAllowanceDeadline(t *testing.T) {
	old := *config.Active()
	t.Cleanup(func() { config.SetActive(old) })
	for _, tc := range []struct {
		mb   int
		want time.Duration
	}{
		{1, 15 * time.Minute},
		{500, 44*time.Minute + 57*time.Second + 152*time.Millisecond},
		{1024, 81*time.Minute + 34*time.Second + 967296*time.Microsecond},
		{10240, 725*time.Minute + 49*time.Second + 672960*time.Microsecond},
	} {
		t.Run(fmt.Sprint(tc.mb), func(t *testing.T) {
			l := old
			l.ImportMaxUploadMB = tc.mb
			config.SetActive(l)
			h, root, caller := newImportTest(t)
			token, _ := mintImportTest(t, h, caller)
			// Malformed multipart stops before conversion, after deadline setup.
			r := httptest.NewRequest("POST", "/v1/imports/"+token, strings.NewReader("bad body"))
			w := &bodyDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			start := time.Now()
			root.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatal(w.Code, w.Body.String())
			}
			checkBodyDeadline(t, w, start, tc.want)
			if len(h.imports.slots) != 0 {
				t.Fatal("receiving admission leaked")
			}
		})
	}
}

func TestKeepJSONBodyAllowanceDeadline(t *testing.T) {
	a, user := keepReviewSite(t)
	user.Handle.String, user.Handle.Valid = "deadline-owner", true
	old := *config.Active()
	t.Cleanup(func() { config.SetActive(old) })
	for _, mb := range []int{1, 500} {
		t.Run(fmt.Sprint(mb), func(t *testing.T) {
			l := old
			l.MaxArchiveOverrides = fmt.Sprintf("deadline-owner:%d", mb)
			config.SetActive(l)
			bodyLimit := (int64(mb)<<20)*4/3 + (1 << 20)
			want := max(15*time.Minute, time.Duration(bodyLimit)*time.Second/250000+10*time.Minute)
			w := &bodyDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
			start := time.Now()
			_, _, limit, override, err := a.sites.decodeJSONFiles(w, keepReviewRequest(user, strings.NewReader(`{}`)))
			if err != nil || !override || limit != int64(mb)<<20 {
				t.Fatal(limit, override, err, w.Body.String())
			}
			checkBodyDeadline(t, w, start, want)
		})
	}
}

func TestImportTARMetadataPayloadBudget(t *testing.T) {
	const valueSize = (64 << 10) - 32
	files, err := unpackImportTest(t, physicalPAX(t, 16, valueSize), config.DefaultLimits())
	if err != nil {
		t.Fatal("payload within metadata budget rejected:", err)
	}
	if want := int64(16 * len(paxBody(strings.Repeat("x", valueSize)))); files.metadata != want {
		t.Fatalf("charged %d metadata bytes, want %d payload bytes", files.metadata, want)
	}
	if _, err := unpackImportTest(t, physicalPAX(t, 21, 64<<10), config.DefaultLimits()); err == nil || !strings.Contains(err.Error(), "extended headers") {
		t.Fatal("oversized metadata payload accepted:", err)
	}
}

func TestImportGNUPAX5000Photos(t *testing.T) {
	var body bytes.Buffer
	w := tar.NewWriter(&body)
	stamp := time.Unix(1700000000, 123456789)
	for i := 0; i <= 5000; i++ {
		name, contents := fmt.Sprintf("photos/%04d.jpg", i), "x"
		if i == 5000 {
			name, contents = "export.xml", testWXR
		}
		// GNU tar --format=pax preserves nanosecond mtime/atime/ctime.
		h := &tar.Header{Name: name, Mode: 0644, Size: int64(len(contents)), Format: tar.FormatPAX,
			ModTime: stamp, AccessTime: stamp, ChangeTime: stamp}
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := unpackImportTest(t, body.Bytes(), config.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if len(files.media) != 5000 || len(files.xml) != 1 || files.entries != 10002 {
		t.Fatalf("media=%d XML=%d physical entries=%d", len(files.media), len(files.xml), files.entries)
	}
}

func TestImportZIP64OrdinaryEOCDFields(t *testing.T) {
	body := makeImportArchive(t, "zip", []importArchiveEntry{{"export.xml", testWXR, 0}})
	end := len(body) - 22
	e := bytes.Clone(body[end:])
	z := make([]byte, 56)
	binary.LittleEndian.PutUint32(z, 0x06064b50)
	binary.LittleEndian.PutUint64(z[4:], 44)
	binary.LittleEndian.PutUint64(z[24:], 1)
	binary.LittleEndian.PutUint64(z[32:], 1)
	binary.LittleEndian.PutUint64(z[40:], uint64(binary.LittleEndian.Uint32(e[12:])))
	binary.LittleEndian.PutUint64(z[48:], uint64(binary.LittleEndian.Uint32(e[16:])))
	locator := make([]byte, 20)
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint64(locator[8:], uint64(end))
	binary.LittleEndian.PutUint32(locator[16:], 1)
	prefix := append(bytes.Clone(body[:end]), z...)
	prefix = append(prefix, locator...)
	for _, tc := range []struct {
		name   string
		change func([]byte)
		valid  bool
	}{
		{"ordinary fields", func([]byte) {}, true},
		{"sentinel count", func(e []byte) {
			binary.LittleEndian.PutUint16(e[8:], 0xffff)
			binary.LittleEndian.PutUint16(e[10:], 0xffff)
		}, true},
		{"sentinel disk count", func(e []byte) { binary.LittleEndian.PutUint16(e[8:], 0xffff) }, true},
		{"sentinel total count", func(e []byte) { binary.LittleEndian.PutUint16(e[10:], 0xffff) }, true},
		{"sentinel size", func(e []byte) { binary.LittleEndian.PutUint32(e[12:], 0xffffffff) }, true},
		{"sentinel offset", func(e []byte) { binary.LittleEndian.PutUint32(e[16:], 0xffffffff) }, true},
		{"count mismatch", func(e []byte) {
			binary.LittleEndian.PutUint16(e[8:], 2)
			binary.LittleEndian.PutUint16(e[10:], 2)
		}, false},
		{"disk count mismatch", func(e []byte) { binary.LittleEndian.PutUint16(e[8:], 2) }, false},
		{"size mismatch", func(e []byte) { binary.LittleEndian.PutUint32(e[12:], 0) }, false},
		{"offset mismatch", func(e []byte) { binary.LittleEndian.PutUint32(e[16:], 0) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := bytes.Clone(e)
			tc.change(e)
			archive := append(bytes.Clone(prefix), e...)
			files, err := unpackImportTest(t, archive, config.DefaultLimits())
			if tc.valid {
				if err != nil || len(files.xml) != 1 {
					t.Fatal("valid ZIP64 rejected:", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "central directory") {
				t.Fatal("inconsistent ZIP64 accepted:", err)
			}
		})
	}
}
