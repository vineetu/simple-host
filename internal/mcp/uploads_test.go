package mcp

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mintUpload(t *testing.T, s *Server) string {
	t.Helper()
	_, out, failed := resultOf(t, send(t, s, toolCall("create_upload_link", map[string]any{"site": "blog"}), nil, true))
	if failed {
		t.Fatal("mint failed")
	}
	u, err := url.Parse(out["upload_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if out["method"] != "PUT" {
		t.Fatal("method")
	}
	if _, err := time.Parse(time.RFC3339, out["expires_at"].(string)); err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(u.Path, "/v1/uploads/")
}

func uploadRequest(s *Server, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/v1/uploads/"+token+"?site=other&publish=false", strings.NewReader("archive"))
	r.SetPathValue("token", token)
	r.RemoteAddr = "203.0.113.2:1234"
	r.Header.Set("X-Forwarded-For", "203.0.113.3")
	r.Header.Set("Content-Type", "application/gzip")
	w := httptest.NewRecorder()
	s.ServeUpload(w, r)
	return w
}

func TestUploadLinkFixedRequestAndSingleUse(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){"PUT /v1/sites/blog": func() (int, string) { return 200, `{"active_version":2}` }}}
	s := newTestServer(up)
	token := mintUpload(t, s)
	if len(up.requests) != 0 {
		t.Fatal("mint called upstream")
	}
	if w := uploadRequest(s, token); w.Code != 200 {
		t.Fatal(w.Code)
	}
	req := up.requests[0]
	if req.Method != "PUT" || req.URL.RequestURI() != "/v1/sites/blog?create=1" || req.Host != "simple-host.app" ||
		req.Header.Get("X-API-Key") != "user-key" || req.Header.Get("X-Skill-Version") != "9.9.9" ||
		req.ContentLength != 7 || req.Header.Get("Content-Type") != "application/gzip" ||
		req.RemoteAddr != "203.0.113.2:1234" || req.Header.Get("X-Forwarded-For") != "203.0.113.3" || up.bodies[0] != "archive" {
		t.Fatal("incorrect inner request")
	}
	for _, tok := range []string{token, "garbage"} {
		w := uploadRequest(s, tok)
		if w.Code != 404 || !strings.Contains(w.Body.String(), "upload_link_invalid") {
			t.Fatal("invalid link accepted")
		}
	}
	if len(up.requests) != 1 {
		t.Fatal("invalid token called upstream")
	}
}

func TestUploadExpiryLimitAndFailedDeploy(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	token := mintUpload(t, s)
	hash := sha256.Sum256([]byte(token))
	entry := s.uploads[hash]
	entry.expires = time.Now().Add(-time.Second)
	s.uploads[hash] = entry
	if w := uploadRequest(s, token); w.Code != 404 {
		t.Fatal("expired accepted")
	}
	token = mintUpload(t, s)
	if w := uploadRequest(s, token); w.Code != 404 {
		t.Fatal("upstream status")
	}
	if _, ok := s.uploads[sha256.Sum256([]byte(token))]; ok {
		t.Fatal("failed deploy retained link")
	}
	for i := 0; i < 20; i++ {
		mintUpload(t, s)
	}
	text, _, failed := resultOf(t, send(t, s, toolCall("create_upload_link", map[string]any{"site": "blog"}), nil, true))
	if !failed || !strings.Contains(text, "20 live upload links") {
		t.Fatal("21st accepted")
	}
	for hash, entry := range s.uploads {
		entry.expires = time.Now().Add(-time.Second)
		s.uploads[hash] = entry
	}
	mintUpload(t, s)
	if len(s.uploads) != 1 {
		t.Fatal("expired links not swept")
	}
}

func TestUploadToolModes(t *testing.T) {
	s := newTestServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, caller := range []Caller{{}, {Mode: CallerModeHackSite}, {Mode: CallerModeEvents}} {
		tools, _, _ := s.forCaller(caller)
		names := map[string]bool{}
		for _, tool := range tools {
			names[tool.Name] = true
		}
		if names["update_site"] != names["create_upload_link"] {
			t.Fatal("tool mode mismatch")
		}
	}
}

func TestUploadLinkUsesSelectedTeamCredential(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){}}
	s := newTestServer(up)
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(toolCall("create_upload_link", map[string]any{"site": "blog"})))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(WithCaller(r.Context(), Caller{APIKey: "person-key", TeamAPIKey: "team-key", Mode: CallerModeEvents, GrantID: "grant"}))
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	_, out, failed := resultOf(t, w)
	if failed {
		t.Fatal("team mint failed")
	}
	u, _ := url.Parse(out["upload_url"].(string))
	uploadRequest(s, strings.TrimPrefix(u.Path, "/v1/uploads/"))
	if len(up.requests) != 1 || up.requests[0].Header.Get("X-API-Key") != "team-key" {
		t.Fatal("upload did not use selected team's key")
	}
}
