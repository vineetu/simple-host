package datatest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

func TestConnectorPhotoLink(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "photo-link")
	other := a.NewPerson(t, "photo-other")
	serialize := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	client := a.RegisterClient(t, servertest.TestRedirect)
	access := a.Connect(t, owner, client, servertest.TestRedirect)["access_token"].(string)
	call := func(name string, args map[string]any) (map[string]any, bool) {
		_, out, failed := servertest.ToolResultOf(t, a.Rpc(t, access, "tools/call", map[string]any{"name": name, "arguments": args}))
		return out, failed
	}
	for _, site := range []string{"blog", "other"} {
		if _, failed := call("create_site", map[string]any{"site": site, "files": map[string]any{"index.html": "<h1>Keep me</h1>"}}); failed {
			t.Fatal("create site")
		}
	}
	if r := a.Do(t, "POST", "/v1/sites/private/files", servertest.JSONBody(map[string]any{"files": map[string]any{"index.html": "private"}}), map[string]string{"X-API-Key": other.Key, "Content-Type": "application/json"}); r.Status >= 400 {
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
	headers := map[string]string{"X-Photo-Token": token, "Content-Type": ct, "Origin": a.Srv.URL}
	// No cookie, connector bearer, or X-API-Key on any photo-link request.
	r := a.Do(t, "GET", "/v1/photo-links", nil, headers)
	if r.Status != 200 || len(r.JSON(t)) != 1 || r.JSON(t)["site"] != "blog" {
		t.Fatal("photo lookup returned account data or failed")
	}
	headers["Origin"] = "https://other.example"
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 403 {
		t.Fatal("wrong Origin accepted")
	}
	headers["Origin"] = "null"
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 403 {
		t.Fatal("opaque Origin accepted")
	}
	headers["Origin"] = a.Srv.URL
	for _, path := range []string{"/v1/sites/other/photos", "/v1/sites/private/photos", "/v1/me", "/v1/sites/blog/files", "/v1/photo-links/other", "/v1/photo-links%2f..%2fsites%2fother%2fphotos"} {
		if r := a.Do(t, "POST", path, strings.NewReader(body), headers); r.Status < 400 {
			t.Fatal("token reached another route")
		}
	}
	// The token proxy shares the process-wide pre-body bound and preserves retry advice.
	t.Setenv("PHOTO_MAX_CONCURRENT", "1")
	if !siteshttp.AcquirePhotoSave() {
		t.Fatal("occupy photo slot")
	}
	func() {
		defer siteshttp.PhotoSaves.Add(-1)
		r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers)
		if r.Status != 429 || r.Header.Get("Retry-After") != "2" {
			t.Fatalf("token bypassed concurrency bound: %d", r.Status)
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
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(many), headers); r.Status != 400 {
		t.Fatalf("token photo count cap: %d", r.Status)
	}
	headers["Content-Type"] = ct
	// Failed validation does not spend a save; actual body caps still apply.
	t.Setenv("PHOTO_MAX_BYTES", "100")
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 413 {
		t.Fatalf("photo cap: %d", r.Status)
	}
	t.Setenv("PHOTO_MAX_BYTES", "1000000")
	for i := 0; i < 10; i++ {
		r := a.Do(t, "POST", "/v1/photo-links?site=other&path=/v1/me", strings.NewReader(body), headers)
		if r.Status != 200 || r.JSON(t)["site"] != "blog" {
			t.Fatalf("photo save %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 404 || r.JSON(t)["code"] != "photo_link_expired" {
		t.Fatal("eleventh save accepted")
	}
	headers["X-Photo-Reconcile"] = "1"
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 200 || r.JSON(t)["version"] != float64(11) || r.JSON(t)["reconciled"] != true {
		t.Fatalf("check tenth save: %d %s", r.Status, r.Body)
	}
	delete(headers, "X-Photo-Reconcile")
	site, failed := call("get_site", map[string]any{"site": "blog"})
	if failed || !strings.Contains(serialize(site), "images/kitchen-10.jpg") || !strings.Contains(serialize(site), "index.html") || !strings.Contains(serialize(site), `"size":`) {
		t.Fatal("get_site missing added photos, sizes, or existing page")
	}
	unchanged, failed := call("get_site", map[string]any{"site": "other"})
	if failed || strings.Contains(serialize(unchanged), "images/") {
		t.Fatal("other site changed")
	}
	headers["X-Photo-Token"] = mint("private")
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status < 400 {
		t.Fatal("another owner's site accepted")
	}
	headers["X-Photo-Token"] = mint("blog")
	// Binding follows the name, including a replacement site in the same account.
	ownerHeaders := map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/json"}
	if r := a.Do(t, "PATCH", "/v1/sites/blog", servertest.JSONBody(map[string]string{"name": "renamed-blog"}), ownerHeaders); r.Status != 200 {
		t.Fatal("rename failed")
	}
	if r := a.Do(t, "POST", "/v1/sites/blog/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "replacement"}}), ownerHeaders); r.Status != 201 {
		t.Fatal("replacement site failed")
	}
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 200 || r.JSON(t)["photos"].([]any)[0].(map[string]any)["path"] != "images/kitchen.jpg" {
		t.Fatal("link did not follow the name to the replacement site")
	}
	if r := a.Do(t, "DELETE", "/v1/me/connections/"+client, nil, map[string]string{"X-API-Key": owner.Key}); r.Status != 204 {
		t.Fatal("disconnect failed")
	}
	if r := a.Do(t, "POST", "/v1/photo-links", strings.NewReader(body), headers); r.Status != 403 {
		t.Fatal("disconnected sign-in accepted")
	}
	// The wrapper rate limits even invalid links, before minting a credential.
	a.Conn.UploadLimiter = httpx.NewRateLimiter(1, 0)
	mux := http.NewServeMux()
	a.Conn.Register(mux, func(next http.Handler) http.Handler { return next })
	for i, want := range []int{404, 429} {
		req := httptest.NewRequest("GET", a.Srv.URL+"/v1/photo-links", nil)
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
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "photo-browser")
	if r := a.Do(t, "POST", "/v1/sites/blog/files", servertest.JSONBody(map[string]any{"files": map[string]any{"index.html": "<h1>Keep me</h1>"}}), map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/json"}); r.Status != 201 {
		t.Fatal("fixture site")
	}
	_, out, failed := servertest.ToolResultOf(t, a.Do(t, "POST", "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog"}}}), map[string]string{"X-API-Key": owner.Key, "Content-Type": "application/json"}))
	if failed {
		t.Fatal("fixture link")
	}
	a.Sites.SetPublicBaseURL(a.Srv.URL)
	a.Data.SetPublicBaseURL(a.Srv.URL)
	siteshttp.RegisterUIRoutes(a.Mux, a.Srv.URL, a.Sites)
	done := make(chan struct{}, 1)
	a.Mux.HandleFunc("POST /test-done", func(w http.ResponseWriter, r *http.Request) {
		select {
		case done <- struct{}{}:
		default:
		}
		w.WriteHeader(204)
	})
	u, _ := url.Parse(out["photos_url"].(string))
	data, _ := json.Marshal(map[string]string{"base": a.Srv.URL, "url": a.Srv.URL + u.RequestURI() + "#" + u.Fragment})
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
