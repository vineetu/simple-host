package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConnectorPhotoLink(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "photo-link")
	other := a.newPerson(t, "photo-other")
	serialize := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, owner, client, testRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (map[string]any, bool) {
		_, out, failed := toolResultOf(t, a.rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
		return out, failed
	}
	for _, site := range []string{"blog", "other"} {
		if _, failed := call("create_site", map[string]any{"site": site, "files": map[string]any{"index.html": "<h1>Keep me</h1>"}}); failed {
			t.Fatal("create site")
		}
	}
	if r := a.do(t, "POST", "/v1/sites/private/files", jsonBody(map[string]any{"files": map[string]any{"index.html": "private"}}), map[string]string{"X-API-Key": other.key, "Content-Type": "application/json"}); r.status >= 400 {
		t.Fatal("create other owner's site")
	}
	mint := func(site string) string {
		t.Helper()
		out, failed := call("create_upload_link", map[string]any{"site": site})
		if failed {
			t.Fatal("photo mint failed")
		}
		u, err := url.Parse(out["photos_url"].(string))
		if err != nil || u.Path != "/photos" || u.Query().Get("site") != site {
			t.Fatal("photo link shape")
		}
		return strings.TrimPrefix(u.Fragment, "t=")
	}
	token := mint("blog")
	body, ct := photoMultipart(t, []string{"kitchen.jpg"}, [][]byte{photoJPEG(t, 8, 6)})
	headers := map[string]string{"X-Photo-Token": token, "Content-Type": ct, "Origin": a.srv.URL}
	// No cookie, connector bearer, or X-API-Key on any photo-link request.
	r := a.do(t, "GET", "/v1/photo-links", nil, headers)
	if r.status != 200 || len(r.json(t)) != 1 || r.json(t)["site"] != "blog" {
		t.Fatal("photo lookup returned account data or failed")
	}
	headers["Origin"] = "https://other.example"
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.status != 403 {
		t.Fatal("wrong Origin accepted")
	}
	headers["Origin"] = "null"
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.status != 403 {
		t.Fatal("opaque Origin accepted")
	}
	headers["Origin"] = a.srv.URL
	for _, path := range []string{"/v1/sites/other/photos", "/v1/sites/private/photos", "/v1/me", "/v1/sites/blog/files", "/v1/photo-links/other", "/v1/photo-links%2f..%2fsites%2fother%2fphotos"} {
		if r := a.do(t, "POST", path, strings.NewReader(body), headers); r.status < 400 {
			t.Fatal("token reached another route")
		}
	}
	// The token proxy shares the process-wide pre-body bound and preserves retry advice.
	t.Setenv("PHOTO_MAX_CONCURRENT", "1")
	if !acquirePhotoSave() {
		t.Fatal("occupy photo slot")
	}
	func() {
		defer photoSaves.Add(-1)
		r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers)
		if r.status != 429 || r.header.Get("Retry-After") != "2" || r.json(t)["error"] != "Busy, try again in a moment" {
			t.Fatalf("token bypassed concurrency bound: %d", r.status)
		}
	}()
	// The link cannot bypass the per-request photo count either.
	names, images := make([]string, 31), make([][]byte, 31)
	for i := range names {
		names[i] = "front.jpg"
		images[i] = photoJPEG(t, 1, 1)
	}
	many, manyCT := photoMultipart(t, names, images)
	headers["Content-Type"] = manyCT
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(many), headers); r.status != 400 {
		t.Fatalf("token photo count cap: %d", r.status)
	}
	headers["Content-Type"] = ct
	// Failed validation does not spend a save; actual body caps still apply.
	t.Setenv("PHOTO_MAX_BYTES", "100")
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.status != 413 {
		t.Fatalf("photo cap: %d", r.status)
	}
	t.Setenv("PHOTO_MAX_BYTES", "1000000")
	for i := 0; i < 10; i++ {
		r := a.do(t, "POST", "/v1/photo-links?site=other&path=/v1/me", strings.NewReader(body), headers)
		if r.status != 200 || r.json(t)["site"] != "blog" {
			t.Fatalf("photo save %d: %d %s", i, r.status, r.body)
		}
	}
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.status != 404 || r.json(t)["error"] != "This photo link has expired. Ask your chat for a new one." {
		t.Fatal("eleventh save accepted")
	}
	site, failed := call("get_site", map[string]any{"site": "blog"})
	if failed || !strings.Contains(serialize(site), "images/kitchen-10.jpg") || !strings.Contains(serialize(site), "index.html") || !strings.Contains(serialize(site), `"size":`) {
		t.Fatal("get_site missing added photos, sizes, or existing page")
	}
	unchanged, failed := call("get_site", map[string]any{"site": "other"})
	if failed || strings.Contains(serialize(unchanged), "images/") {
		t.Fatal("other site changed")
	}
	headers["X-Photo-Token"] = mint("private")
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.status < 400 {
		t.Fatal("another owner's site accepted")
	}
	headers["X-Photo-Token"] = mint("blog")
	if r := a.do(t, "DELETE", "/v1/me/connections/"+client, nil, map[string]string{"X-API-Key": owner.key}); r.status != 204 {
		t.Fatal("disconnect failed")
	}
	if r := a.do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.status != 403 {
		t.Fatal("disconnected sign-in accepted")
	}
	// The wrapper rate limits even invalid links, before minting a credential.
	a.conn.uploadLimiter = newRateLimiter(1, 0)
	mux := http.NewServeMux()
	a.conn.Register(mux, func(next http.Handler) http.Handler { return next })
	for i, want := range []int{404, 429} {
		req := httptest.NewRequest("GET", a.srv.URL+"/v1/photo-links", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("rate limit request %d: %d", i, w.Code)
		}
	}
}

// Optional browser fixture: the private file carries the link, never test output.
func TestServePhotoLinkForBrowser(t *testing.T) {
	fixture := os.Getenv("PHOTO_LINK_BROWSER_FIXTURE")
	if fixture == "" {
		t.Skip("PHOTO_LINK_BROWSER_FIXTURE unset")
	}
	a := newConnectorApp(t)
	owner := a.newPerson(t, "photo-browser")
	if r := a.do(t, "POST", "/v1/sites/blog/files", jsonBody(map[string]any{"files": map[string]any{"index.html": "<h1>Keep me</h1>"}}), map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}); r.status != 201 {
		t.Fatal("fixture site")
	}
	_, out, failed := toolResultOf(t, a.do(t, "POST", "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog"}}}), map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}))
	if failed {
		t.Fatal("fixture link")
	}
	a.sites.SetPublicBaseURL(a.srv.URL)
	RegisterUIRoutes(a.mux, a.srv.URL, a.sites)
	done := make(chan struct{}, 1)
	a.mux.HandleFunc("POST /test-done", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	u, _ := url.Parse(out["photos_url"].(string))
	data, _ := json.Marshal(map[string]string{"base": a.srv.URL, "url": a.srv.URL + u.RequestURI() + "#" + u.Fragment})
	if err := os.WriteFile(fixture, data, 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(fixture)
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
