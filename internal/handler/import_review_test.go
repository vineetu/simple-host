package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/mcp"
)

func TestImportZIPDirectoryPreflight(t *testing.T) {
	body := makeImportArchive(t, "zip", []importArchiveEntry{{"a/", "", 0}, {"b/", "", 0}})
	end := len(body) - 22
	for _, tc := range []struct {
		name  string
		count uint16
		limit int
	}{{"declared", 2, 1}, {"dishonest", 1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			b := bytes.Clone(body)
			binary.LittleEndian.PutUint16(b[end+8:], tc.count)
			binary.LittleEndian.PutUint16(b[end+10:], tc.count)
			if _, err := checkImportZIP(bytes.NewReader(b), int64(len(b)), tc.limit); err == nil {
				t.Fatal("unbounded directory accepted")
			}
		})
	}
	// archive/zip selects even an EOCD-looking record embedded in a comment.
	commented := bytes.Clone(body)
	binary.LittleEndian.PutUint16(commented[end+20:], 24)
	fake := make([]byte, 24)
	copy(fake, []byte{'P', 'K', 5, 6})
	commented = append(commented, fake...)
	if _, err := checkImportZIP(bytes.NewReader(commented), int64(len(commented)), 20000); err == nil {
		t.Fatal("ambiguous ZIP end record accepted")
	}
	// Directory bytes are bounded even when its record count is small.
	b := bytes.Clone(body)
	binary.LittleEndian.PutUint32(b[end+12:], (4<<20)+1)
	if _, err := checkImportZIP(bytes.NewReader(b), int64(len(b)), 20000); err == nil || !strings.Contains(err.Error(), "metadata budget") {
		t.Fatal(err)
	}
	// ZIP64 uses the locator's end record rather than the truncated ZIP count.
	z := make([]byte, 56)
	binary.LittleEndian.PutUint32(z, 0x06064b50)
	binary.LittleEndian.PutUint64(z[4:], 44)
	binary.LittleEndian.PutUint64(z[24:], 2)
	binary.LittleEndian.PutUint64(z[32:], 2)
	binary.LittleEndian.PutUint64(z[40:], uint64(binary.LittleEndian.Uint32(body[end+12:])))
	binary.LittleEndian.PutUint64(z[48:], uint64(binary.LittleEndian.Uint32(body[end+16:])))
	loc := make([]byte, 20)
	binary.LittleEndian.PutUint32(loc, 0x07064b50)
	binary.LittleEndian.PutUint64(loc[8:], uint64(end))
	binary.LittleEndian.PutUint32(loc[16:], 1)
	b = append(bytes.Clone(body[:end]), z...)
	b = append(b, loc...)
	endRecord := bytes.Clone(body[end:])
	binary.LittleEndian.PutUint16(endRecord[8:], 0xffff)
	binary.LittleEndian.PutUint16(endRecord[10:], 0xffff)
	binary.LittleEndian.PutUint32(endRecord[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(endRecord[16:], 0xffffffff)
	b = append(b, endRecord...)
	if _, err := checkImportZIP(bytes.NewReader(b), int64(len(b)), 1); err == nil {
		t.Fatal("ZIP64 count accepted")
	}
	if _, err := checkImportZIP(bytes.NewReader(b), int64(len(b)), 2); err != nil {
		t.Fatal("valid ZIP64 rejected", err)
	}
}

func TestImportEffectivePathLimit(t *testing.T) {
	if _, err := importPath(strings.Repeat("x", 1024)); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"zip", "pax", "gnu"} {
		t.Run(format, func(t *testing.T) {
			name := strings.Repeat("x", 1025)
			var body []byte
			if format == "zip" {
				body = makeImportArchive(t, "zip", []importArchiveEntry{{name, "", 0}})
			} else {
				var b bytes.Buffer
				w := tar.NewWriter(&b)
				f := tar.FormatPAX
				if format == "gnu" {
					f = tar.FormatGNU
				}
				if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0644, Format: f}); err != nil {
					t.Fatal(err)
				}
				w.Close()
				body = b.Bytes()
			}
			if _, err := unpackImportTest(t, body, config.DefaultLimits()); err == nil || !strings.Contains(err.Error(), "1024") {
				t.Fatal(err)
			}
		})
	}
}

func paxBody(value string) string {
	text := " comment=" + value + "\n"
	n := len(text) + 1
	for {
		record := fmt.Sprint(n) + text
		if len(record) == n {
			return record
		}
		n = len(record)
	}
}
func physicalPAX(t *testing.T, count, valueSize int) []byte {
	t.Helper()
	var b bytes.Buffer
	for i := 0; i < count; i++ {
		content := paxBody(strings.Repeat("x", valueSize))
		part := makeImportArchive(t, "tar", []importArchiveEntry{{"pax", content, 0}})
		h := part[:512]
		h[156] = tar.TypeXHeader
		for j := 148; j < 156; j++ {
			h[j] = ' '
		}
		sum := 0
		for _, c := range h {
			sum += int(c)
		}
		copy(h[148:156], fmt.Sprintf("%06o\x00 ", sum))
		b.Write(part[:512+((len(content)+511)/512)*512])
	}
	b.Write(makeImportArchive(t, "tar", []importArchiveEntry{{"export.xml", testWXR, 0}}))
	return b.Bytes()
}
func gzipImport(t *testing.T, body []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write(body)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func TestImportTARPhysicalHeadersAndStreamBudget(t *testing.T) {
	t.Run("PAX boundary override", func(t *testing.T) {
		body := bytes.Replace(physicalPAX(t, 1, 1), []byte("comment=x"), []byte("size=0000"), 1)
		if _, err := unpackImportTest(t, body, config.DefaultLimits()); err == nil || !strings.Contains(err.Error(), "overrides") {
			t.Fatal(err)
		}
	})

	t.Run("physical entries", func(t *testing.T) {
		l := config.DefaultLimits()
		l.ImportMaxArchiveEntries = 2
		if _, err := unpackImportTest(t, gzipImport(t, physicalPAX(t, 3, 1)), l); err == nil || !strings.Contains(err.Error(), "physical entries") {
			t.Fatal(err)
		}
	})
	t.Run("extended budget", func(t *testing.T) {
		if _, err := unpackImportTest(t, physicalPAX(t, 17, 64<<10), config.DefaultLimits()); err == nil || !strings.Contains(err.Error(), "extended headers") {
			t.Fatal(err)
		}
	})
	for _, format := range []string{"tar", "tgz"} {
		t.Run(format+" stream", func(t *testing.T) {
			l := config.DefaultLimits()
			l.ImportMaxUnpackedMB = 1
			body := physicalPAX(t, 15, 64<<10)
			body = append(body, bytes.Repeat([]byte{' '}, 64<<10)...)
			if format == "tgz" {
				body = gzipImport(t, body)
			}
			if _, err := unpackImportTest(t, body, l); err == nil || !strings.Contains(err.Error(), "too large") {
				t.Fatal(err)
			}
		})
	}
}
func TestImportGzipCumulativeRatio(t *testing.T) {
	entries := []importArchiveEntry{{"export.xml", testWXR, 0}}
	for i := 0; i < 12; i++ {
		entries = append(entries, importArchiveEntry{fmt.Sprintf("photo%d.jpg", i), strings.Repeat("x", 1<<20), 0})
	}
	if _, err := unpackImportTest(t, makeImportArchive(t, "tgz", entries), config.DefaultLimits()); err == nil || !strings.Contains(err.Error(), "ratio") {
		t.Fatal(err)
	}
}

// Use the real HTTP read deadline, shortened for a stalled-body regression.
type shortImportDeadline struct {
	http.ResponseWriter
	set chan time.Time
}

func (w shortImportDeadline) SetReadDeadline(deadline time.Time) error {
	if !deadline.IsZero() {
		w.set <- deadline
		deadline = time.Now().Add(200 * time.Millisecond)
	}
	return http.NewResponseController(w.ResponseWriter).SetReadDeadline(deadline)
}
func TestImportReceivingAdmissionAndDeadline(t *testing.T) {
	h, root, c := newImportTest(t)
	root = NoticeMiddleware("2.0.0")(root)
	set := make(chan time.Time, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root.ServeHTTP(&statusCapture{ResponseWriter: shortImportDeadline{w, set}, status: http.StatusOK}, r)
	}))
	defer server.Close()
	results := make(chan int, cap(h.imports.slots))
	var bodies []*io.PipeWriter
	for i := 0; i < cap(h.imports.slots); i++ {
		token, _ := mintImportTest(t, h, c)
		read, write := io.Pipe()
		bodies = append(bodies, write)
		req, _ := http.NewRequest("POST", server.URL+"/v1/imports/"+token, read)
		req.Header.Set("Content-Type", "multipart/form-data; boundary=stalled")
		req.Header.Set("X-Skill-Version", "1.0.0")
		go func() {
			res, err := server.Client().Do(req)
			if err != nil {
				results <- 0
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			results <- res.StatusCode
		}()
		select {
		case deadline := <-set:
			if time.Until(deadline) < 14*time.Minute {
				t.Fatal("no receiving deadline")
			}
		case <-time.After(time.Second):
			t.Fatal("receiver not admitted")
		}
	}
	defer func() {
		for _, b := range bodies {
			b.Close()
		}
	}()
	token, _ := mintImportTest(t, h, c)
	if w := importPOST(t, root, token, testWXR); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, ok := h.mcp.ImportLinkForPage(token); !ok {
		t.Fatal("busy consumed link")
	}
	for i := 0; i < cap(h.imports.slots); i++ {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("stalled body retained slot")
		}
	}
	if len(h.imports.slots) != 0 {
		t.Fatal("admission leaked")
	}
	for _, r := range h.imports.records {
		if r.state != "waiting" || !r.started.IsZero() {
			t.Fatal("reservation not reset", r.state)
		}
	}
}

func TestImportRecordsPreserveLiveLinksAndEvictFinished(t *testing.T) {
	for _, state := range []string{"waiting", "receiving", "converting", "publishing", "queued"} {
		t.Run(state, func(t *testing.T) {
			m := newImportManager()
			now := time.Now()
			expires := now.Add(-time.Hour)
			started := now
			if state == "waiting" {
				expires = now.Add(time.Hour)
				started = time.Time{}
			}
			m.records["live"] = &importRecord{owner: "owner", created: now.Add(-time.Hour), expires: expires, started: started, state: state}
			for i := 0; i < 19; i++ {
				m.records[fmt.Sprint(i)] = &importRecord{owner: "owner", created: now, finished: now.Add(time.Duration(i)*time.Minute - 20*time.Minute), state: "done"}
			}
			if err := m.created(mcp.ImportLink{ID: "new", Owner: "owner", Expires: now.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if m.records["live"] == nil || m.records["0"] != nil || len(m.records) != 20 {
				t.Fatal("wrong report evicted")
			}
		})
	}
}
func TestImportRecordsSweepExpiredUnused(t *testing.T) {
	m := newImportManager()
	past := time.Now().Add(-time.Hour)
	for _, state := range []string{"waiting", "receiving", "converting", "publishing"} {
		started := time.Time{}
		if state != "waiting" {
			started = time.Now()
		}
		m.records[state] = &importRecord{state: state, expires: past, started: started}
	}
	m.sweepLocked()
	if m.records["waiting"] != nil || len(m.records) != 3 {
		t.Fatal("expired unused record retained or active job removed")
	}
}
func TestImportStatusLimiterAndRetryAfter(t *testing.T) {
	h, root, c := newImportTest(t)
	_, id := mintImportTest(t, h, c)
	now := time.Now()
	h.importStatusLimiter.now = func() time.Time { return now }
	for i := 0; i < 60; i++ {
		if w := importGET(root, "/v1/imports/status/"+id); w.Code != 200 {
			t.Fatal(i, w.Code)
		}
	}
	w := importGET(root, "/v1/imports/status/"+id)
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" {
		t.Fatal(w.Code, w.Header())
	}
	token, _ := mintImportTest(t, h, c)
	if w := importPOST(t, root, token, "invalid"); w.Code != 400 {
		t.Fatal("status throttled upload", w.Code)
	}
	now = now.Add(time.Second)
	if w := importGET(root, "/v1/imports/status/"+id); w.Code != 200 {
		t.Fatal("no read refill")
	}
	raw, _ := staticFiles.ReadFile("static/import.html")
	if !bytes.Contains(raw, []byte("res.status===429")) || !bytes.Contains(raw, []byte("res.headers.get('Retry-After')")) {
		t.Fatal("page ignores Retry-After")
	}
}
func TestImportLinkUsesDeploySiteShape(t *testing.T) {
	h, _, c := newImportTest(t)
	for _, name := range []string{"-", "blog-", "-blog", strings.Repeat("a", 64)} {
		if _, failed, _ := importTool(t, h, c, "create_import_link", map[string]any{"site": name}); !failed {
			t.Fatal("invalid site link", name)
		}
	}
	for _, name := range []string{"a", strings.Repeat("a", 63), "api"} {
		if _, failed, body := importTool(t, h, c, "create_import_link", map[string]any{"site": name}); failed {
			t.Fatal("shape-valid/reserved existing site", name, body)
		}
	}
}
