package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/mcp"
	"github.com/vsriram/simple-host/internal/wpimport"
)

const testWXR = `<?xml version="1.0"?><rss xmlns:wp="http://wordpress.org/export/1.2/"><channel><wp:base_site_url>https://old.example</wp:base_site_url><wp:base_blog_url>https://www.old.example</wp:base_blog_url><item/></channel></rss>`

func importTool(t *testing.T, h *ConnectorHandler, caller mcp.Caller, name string, args map[string]any) (map[string]any, bool, string) {
	t.Helper()
	body := jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	r := httptest.NewRequest("POST", "/mcp", body)
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(mcp.WithCaller(r.Context(), caller))
	w := httptest.NewRecorder()
	h.mcp.ServeHTTP(w, r)
	var rpc struct {
		Result struct {
			Structured map[string]any `json:"structuredContent"`
			IsError    bool           `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rpc); err != nil {
		t.Fatal(err)
	}
	return rpc.Result.Structured, rpc.Result.IsError, w.Body.String()
}
func newImportTest(t *testing.T) (*ConnectorHandler, http.Handler, mcp.Caller) {
	t.Helper()
	h := NewConnectorHandler(nil, "https://simple-host.test", "", "simple-host.test", "sites.simple-host.test", "1", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 201, map[string]any{"site_url": "https://blog.person.simple-host.test/", "active_version": 1})
	}))
	mux := http.NewServeMux()
	h.Register(mux, func(h http.Handler) http.Handler { return h })
	return h, CORS(mux), mcp.Caller{APIKey: "test-key", Owner: "owner", MintUpload: func(context.Context, bool) (string, func(), error) { return "fresh", func() {}, nil }}
}
func mintImportTest(t *testing.T, h *ConnectorHandler, c mcp.Caller) (string, string) {
	t.Helper()
	out, failed, body := importTool(t, h, c, "create_import_link", map[string]any{"site": "blog", "theme_url": "https://old.example/"})
	if failed {
		t.Fatal(body)
	}
	u, _ := url.Parse(out["import_url"].(string))
	return strings.TrimPrefix(u.Path, "/import/"), out["import_id"].(string)
}
func importMultipart(t *testing.T, body string) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	p, err := mw.CreateFormFile("files", "export.xml")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(p, body)
	mw.Close()
	return &b, mw.FormDataContentType()
}
func importPOST(t *testing.T, root http.Handler, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	b, ct := importMultipart(t, body)
	r := httptest.NewRequest("POST", "https://simple-host.test/v1/imports/"+token, b)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	root.ServeHTTP(w, r)
	return w
}
func importGET(root http.Handler, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "https://simple-host.test"+path, nil)
	w := httptest.NewRecorder()
	root.ServeHTTP(w, r)
	return w
}
func fakeImport(t *testing.T, fn func(context.Context, wpimport.Input) (*wpimport.Result, error)) {
	t.Helper()
	old := convertImport
	convertImport = fn
	t.Cleanup(func() { convertImport = old })
}
func waitImport(t *testing.T, h *ConnectorHandler, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := h.imports.report("owner", id)
		if err != nil {
			t.Fatal(err)
		}
		if out["state"] == "done" || out["state"] == "failed" {
			return out
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("import did not finish")
	return nil
}
func TestImportLifecycle(t *testing.T) {
	h, root, c := newImportTest(t)
	token, id := mintImportTest(t, h, c)
	for _, p := range []struct {
		path string
		code int
	}{{"/import/" + token, 200}, {"/import/unknown", 404}} {
		w := importGET(root, p.path)
		if w.Code != p.code {
			t.Fatal(w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Fatal("page headers")
		}
	}
	page := importGET(root, "/import/"+token)
	for _, want := range []string{"Move your WordPress site", "old.example", "<!--sh:", "window.shTheme={", "nonce-"} {
		if want == "<!--sh:" {
			if strings.Contains(page.Body.String(), want) {
				t.Fatal("unexpanded chrome")
			}
			continue
		}
		if want == "nonce-" {
			if !strings.Contains(page.Header().Get("Content-Security-Policy"), want) {
				t.Fatal("no CSP")
			}
			continue
		}
		if !strings.Contains(page.Body.String(), want) {
			t.Fatal("missing", want)
		}
	}
	if w := importPOST(t, root, token, "not an archive"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	fakeImport(t, func(ctx context.Context, in wpimport.Input) (*wpimport.Result, error) {
		return &wpimport.Result{Files: map[string][]byte{"index.html": []byte("hello"), "about.html": []byte("about")}, Report: wpimport.Report{Pages: 2, Posts: 3, MediaCopied: 4, Warnings: []string{"Photo was missing"}, Features: []wpimport.Feature{{Kind: "comments", Status: "needs_setup", Pages: []string{"/about.html"}, Setup: []wpimport.ToolCall{{Tool: "storage_set_resource", Args: map[string]any{"name": "comments"}}}}}}}, nil
	})
	w := importPOST(t, root, token, testWXR)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := importPOST(t, root, token, testWXR); w.Code != 404 && w.Code != 409 {
		t.Fatal("reserved or spent link", w.Code)
	}
	out := waitImport(t, h, id)
	if importPOST(t, root, token, testWXR).Code != 404 {
		t.Fatal("success did not spend link")
	}
	if out["state"] != "done" {
		t.Fatal(out)
	}
	if importGET(root, "/import/"+token).Code != 404 {
		t.Fatal("used page")
	}
	w = importGET(root, "/v1/imports/status/"+id)
	var public map[string]any
	json.Unmarshal(w.Body.Bytes(), &public)
	if public["pages"] != float64(2) || public["posts"] != float64(3) || public["media"] != float64(4) || len(public) != 10 {
		t.Fatal("public shape", public)
	}
	for _, forbidden := range []string{"report", "owner", "files", "site", "import_url"} {
		if _, ok := public[forbidden]; ok {
			t.Fatal("leaked", forbidden)
		}
	}
	if !strings.Contains(out["next_steps"].([]string)[0], "storage_set_resource") {
		t.Fatal(out)
	}
	_, failed, body := importTool(t, h, mcp.Caller{APIKey: "other", Owner: "other"}, "get_import_report", map[string]any{"import_id": id})
	if !failed || !strings.Contains(body, "no import with that id for this account") {
		t.Fatal(body)
	}
}
func TestImportSignedOutExpiryAndCap(t *testing.T) {
	h, root, c := newImportTest(t)
	c.MintUpload = func(context.Context, bool) (string, func(), error) { return "", nil, errors.New("ended") }
	token, _ := mintImportTest(t, h, c)
	w := importPOST(t, root, token, testWXR)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "import_link_signed_out") {
		t.Fatal(w.Code, w.Body.String())
	}
	if importPOST(t, root, token, testWXR).Code != 404 {
		t.Fatal("signed out token survived")
	}
	for i := 0; i < 20; i++ {
		mintImportTest(t, h, c)
	}
	_, failed, _ := importTool(t, h, c, "create_import_link", map[string]any{"site": "blog"})
	if !failed {
		t.Fatal("21st link accepted")
	}
	limits := *config.Active()
	old := limits
	limits.ImportLinkTTL = time.Nanosecond
	config.SetActive(limits)
	t.Cleanup(func() { config.SetActive(old) })
	h, root, c = newImportTest(t)
	token, _ = mintImportTest(t, h, c)
	if importGET(root, "/import/"+token).Code != 404 || importPOST(t, root, token, testWXR).Code != 404 {
		t.Fatal("expired link accepted")
	}
}
func TestImportConcurrentPostAndEntityRetry(t *testing.T) {
	h, root, c := newImportTest(t)
	token, id := mintImportTest(t, h, c)
	if w := importPOST(t, root, token, `<?xml version="1.0"?><!DOCTYPE rss [<!ENTITY x "y">]><rss/>`); w.Code != 400 || !strings.Contains(w.Body.String(), "entity") {
		t.Fatal(w.Code, w.Body.String())
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	c.MintUpload = func(context.Context, bool) (string, func(), error) {
		close(entered)
		<-release
		return "fresh", func() {}, nil
	}
	token, id = mintImportTest(t, h, c)
	fakeImport(t, func(context.Context, wpimport.Input) (*wpimport.Result, error) {
		return nil, errors.New("plain conversion failure")
	})
	result := make(chan *httptest.ResponseRecorder)
	go func() { result <- importPOST(t, root, token, testWXR) }()
	<-entered
	if w := importPOST(t, root, token, testWXR); w.Code != 409 || !strings.Contains(w.Body.String(), "already uploading") {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	if w := <-result; w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if waitImport(t, h, id)["state"] != "failed" {
		t.Fatal("failure not recorded")
	}
}
func TestImportQueueAndCleanup(t *testing.T) {
	h, root, c := newImportTest(t)
	started := make(chan struct{})
	release := make(chan struct{})
	var first bool
	fakeImport(t, func(context.Context, wpimport.Input) (*wpimport.Result, error) {
		if !first {
			first = true
			close(started)
		}
		<-release
		return nil, errors.New("test failure")
	})
	var ids []string
	for i := 0; i < 6; i++ {
		token, id := mintImportTest(t, h, c)
		ids = append(ids, id)
		if w := importPOST(t, root, token, testWXR); w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		if i == 0 {
			<-started
		}
	}
	token, _ := mintImportTest(t, h, c)
	if w := importPOST(t, root, token, testWXR); w.Code != 503 {
		t.Fatal(w.Code, w.Body.String())
	}
	if importGET(root, "/import/"+token).Code != 200 {
		t.Fatal("busy spent link")
	}
	close(release)
	for _, id := range ids {
		waitImport(t, h, id)
	}
	h.imports.mu.Lock()
	r := h.imports.records[ids[0]]
	r.finished = time.Now().Add(-25 * time.Hour)
	h.imports.mu.Unlock()
	if importGET(root, "/v1/imports/status/"+ids[0]).Code != 404 {
		t.Fatal("old record retained")
	}
}
func TestImportSameOrigin(t *testing.T) {
	h, root, c := newImportTest(t)
	token, _ := mintImportTest(t, h, c)
	for _, method := range []string{"POST", "OPTIONS"} {
		b, ct := importMultipart(t, testWXR)
		r := httptest.NewRequest(method, "https://simple-host.test/v1/imports/"+token, b)
		r.Header.Set("Origin", "https://third-party.example")
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		root.ServeHTTP(w, r)
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS grant")
		}
		if method == "POST" && w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if importGET(root, "/import/"+token).Code != 200 {
		t.Fatal("origin rejection spent link")
	}
}

func TestImportCleanupAndDeployRefusal(t *testing.T) {
	for _, mode := range []string{"panic", "refused", "done"} {
		t.Run(mode, func(t *testing.T) {
			h, root, c := newImportTest(t)
			revoked := make(chan struct{})
			minted, revokeCount := 0, 0
			c.MintUpload = func(context.Context, bool) (string, func(), error) {
				minted++
				return "fresh", func() {
					revokeCount++
					if revokeCount == minted {
						close(revoked)
					}
				}, nil
			}
			if mode == "refused" {
				h.upstream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, 413, errorResponse{Error: "site storage is full"})
				})
			}
			var directory string
			fakeImport(t, func(ctx context.Context, in wpimport.Input) (*wpimport.Result, error) {
				directory = filepath.Dir(in.WXR[0].(*os.File).Name())
				if mode == "panic" {
					panic("converter panic")
				}
				return &wpimport.Result{Files: map[string][]byte{"index.html": []byte("hello")}}, nil
			})
			token, id := mintImportTest(t, h, c)
			if w := importPOST(t, root, token, testWXR); w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			out := waitImport(t, h, id)
			select {
			case <-revoked:
			case <-time.After(time.Second):
				t.Fatal("credential not revoked")
			}
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(directory); os.IsNotExist(err) {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if _, err := os.Stat(directory); !os.IsNotExist(err) {
				t.Fatal("temp directory remains", err)
			}
			if mode == "done" && out["state"] != "done" {
				t.Fatal(out)
			}
			if mode == "refused" && (out["state"] != "failed" || out["message"] != "site storage is full") {
				t.Fatal(out)
			}
			if mode == "panic" && out["state"] != "failed" {
				t.Fatal(out)
			}
		})
	}
}
func TestImportReceivingCapsReleaseReservation(t *testing.T) {
	old := *config.Active()
	limits := old
	limits.ImportMaxUploadMB = 1
	config.SetActive(limits)
	t.Cleanup(func() { config.SetActive(old) })
	h, root, c := newImportTest(t)
	token, _ := mintImportTest(t, h, c)
	if w := importPOST(t, root, token, testWXR+strings.Repeat("x", 1<<20)); w.Code != 413 || !strings.Contains(w.Body.String(), "Upload the WordPress export on its own") {
		t.Fatal(w.Code, w.Body.String())
	}
	// The page knows the limit, so it can explain an oversized choice before
	// uploading (the photographer test site's 1.5 GB archive got an HTML 413).
	if page := importGET(root, "/import/"+token); page.Code != 200 || !strings.Contains(page.Body.String(), `data-max-mb="1"`) {
		t.Fatal("large upload spent link, or the page does not carry the size limit")
	}
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for i := 0; i < 11; i++ {
		p, _ := mw.CreateFormFile("files", "export.xml")
		io.WriteString(p, testWXR)
	}
	mw.Close()
	r := httptest.NewRequest("POST", "https://simple-host.test/v1/imports/"+token, &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	root.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "10 files") {
		t.Fatal(w.Code, w.Body.String())
	}
	if importGET(root, "/import/"+token).Code != 200 {
		t.Fatal("too many files spent link")
	}
}
func TestImportRecordCapAndFeatures(t *testing.T) {
	m := newImportManager()
	for i := 0; i < 21; i++ {
		id := fmt.Sprint(i)
		if err := m.created(mcp.ImportLink{ID: id, Owner: "owner", Site: "blog"}); err != nil {
			t.Fatal(err)
		}
		m.update(id, func(r *importRecord) { r.finished = time.Now(); r.state = "done" })
	}
	if len(m.records) != 20 || m.records["0"] != nil {
		t.Fatal("oldest record not dropped")
	}
	m.update("20", func(r *importRecord) {
		r.report = &wpimport.Report{Features: []wpimport.Feature{{Kind: "search", Status: wpimport.StatusBuiltIn, Use: "Built-in search", Note: "Ready"}, {Kind: "hit_counter", Status: wpimport.StatusReplaced, Use: "site_analytics", Note: "Ask in chat"}, {Kind: "shop", Status: wpimport.StatusNotSupported, Use: "Static catalog", Note: "No checkout"}}}
	})
	out, err := m.report("owner", "20")
	if err != nil {
		t.Fatal(err)
	}
	steps := out["next_steps"].([]string)
	if len(steps) != 4 || !strings.Contains(steps[0], "Built-in search") || !strings.Contains(steps[1], "site_analytics") || !strings.Contains(steps[2], "No checkout") || !strings.Contains(steps[3], "connect_domain") {
		t.Fatal(steps)
	}
}

func TestImportThemeHostAndPhotoSelection(t *testing.T) {
	h, root, c := newImportTest(t)
	fakeImport(t, func(ctx context.Context, in wpimport.Input) (*wpimport.Result, error) {
		if in.ThemeURL != "" {
			t.Error("unrelated theme host accepted")
		}
		if _, err := in.Fetch(ctx, "https://old.example/photo.jpg"); err == nil || !strings.Contains(err.Error(), "switched off") {
			t.Error("photo setting not passed", err)
		}
		return &wpimport.Result{Files: map[string][]byte{"index.html": []byte("hello")}}, nil
	})
	token, id := mintImportTest(t, h, c)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	part, _ := mw.CreateFormFile("files", "export.xml")
	io.WriteString(part, testWXR)
	mw.WriteField("theme_url", "https://unrelated.example/")
	mw.WriteField("copy_photos", "false")
	mw.Close()
	r := httptest.NewRequest("POST", "https://simple-host.test/v1/imports/"+token, &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	root.ServeHTTP(w, r)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	out := waitImport(t, h, id)
	if out["state"] != "done" || len(out["report"].(*wpimport.Report).Warnings) != 1 {
		t.Fatal(out)
	}
}

func TestImportRechecksSignInBeforePublishing(t *testing.T) {
	h, root, c := newImportTest(t)
	calls := 0
	revoked := make(chan struct{})
	deployed := false
	c.MintUpload = func(context.Context, bool) (string, func(), error) {
		calls++
		if calls == 2 {
			return "", nil, errors.New("connection ended")
		}
		return "first", func() { close(revoked) }, nil
	}
	h.upstream = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { deployed = true })
	fakeImport(t, func(context.Context, wpimport.Input) (*wpimport.Result, error) {
		return &wpimport.Result{Files: map[string][]byte{"index.html": []byte("hello")}}, nil
	})
	token, id := mintImportTest(t, h, c)
	if w := importPOST(t, root, token, testWXR); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	out := waitImport(t, h, id)
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("original credential retained")
	}
	if out["state"] != "failed" || !strings.Contains(out["message"].(string), "sign-in ended") || deployed || calls != 2 {
		t.Fatal(out, calls, deployed)
	}
}
