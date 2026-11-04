package handler

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	results := make(chan int, cap(h.imports.Slots))
	var bodies []*io.PipeWriter
	for i := 0; i < cap(h.imports.Slots); i++ {
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
	for i := 0; i < cap(h.imports.Slots); i++ {
		select {
		case <-results:
		case <-time.After(2 * time.Second):
			t.Fatal("stalled body retained slot")
		}
	}
	if len(h.imports.Slots) != 0 {
		t.Fatal("admission leaked")
	}
	for _, r := range h.imports.Records {
		if r.State != "waiting" || !r.Started.IsZero() {
			t.Fatal("reservation not reset", r.State)
		}
	}
}

func TestImportStatusLimiterAndRetryAfter(t *testing.T) {
	h, root, c := newImportTest(t)
	_, id := mintImportTest(t, h, c)
	now := time.Now()
	h.importStatusLimiter.Now = func() time.Time { return now }
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
