package mcp

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mintPhoto(t *testing.T, s *Server, caller Caller) string {
	t.Helper()
	out, err := createUploadLink(&call{server: s, caller: caller}, map[string]any{"site": "blog"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(out.Structured["photos_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "https" || u.Host != s.cfg.APIHost || u.Path != "/photos" || u.RawQuery != "site=blog" || len(out.Structured) != 4 || out.Structured["folder"] != "images/" || !strings.Contains(out.Structured["next"].(string), "keep_other_files: true") {
		t.Fatal("wrong photo result shape")
	}
	expiry, err := time.Parse(time.RFC3339, out.Structured["expires_at"].(string))
	if err != nil || time.Until(expiry) < 59*time.Minute || time.Until(expiry) > time.Hour {
		t.Fatal("wrong expiry")
	}
	return strings.TrimPrefix(u.Fragment, "t=")
}
func photoRequest(s *Server, token, method string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/v1/photo-links?site=other&path=/v1/me&create=1", strings.NewReader("photos"))
	r.Header.Set("X-Photo-Token", token)
	r.Header.Set("X-API-Key", "attacker")
	r.Header.Set("Cookie", "account=attacker")
	r.Header.Set("Origin", "https://"+s.cfg.APIHost)
	r.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	w := httptest.NewRecorder()
	s.ServePhotoLink(w, r)
	return w
}
func TestPhotoLinkFixedRequestAndCap(t *testing.T) {
	calls, minted, revoked := 0, 0, 0
	s := NewServer(Config{APIHost: "box.example.org", Upstream: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.RequestURI() != "/v1/sites/blog/photos" || r.Host != "box.example.org" || r.Header.Get("X-API-Key") != "fresh" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Photo-Token") != "" || r.Header.Get("Origin") != "https://box.example.org" || r.ContentLength != 6 {
			t.Error("photo proxy escaped its boundary")
		}
		w.Header().Set("X-API-Key", "must-not-escape")
		w.WriteHeader(200)
	})})
	token := mintPhoto(t, s, Caller{Owner: "owner", MintUpload: func(context.Context, bool) (string, func(), error) {
		minted++
		return "fresh", func() { revoked++ }, nil
	}})
	if minted != 0 {
		t.Fatal("minted too early")
	}
	if w := uploadRequest(s, token); w.Code != 404 {
		t.Fatal("photo token accepted for archive")
	}
	if w := photoRequest(s, token, "GET"); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"site":"blog"}` {
		t.Fatal("page lookup leaked data or failed")
	}
	for i := 0; i < 10; i++ {
		if w := photoRequest(s, token, "POST"); w.Code != 200 || w.Header().Get("X-API-Key") != "" {
			t.Fatal("save or header filter failed")
		}
	}
	if w := photoRequest(s, token, "POST"); w.Code != 404 || !strings.Contains(w.Body.String(), photoLinkExpired) {
		t.Fatal("save cap not enforced")
	}
	if calls != 10 || minted != 11 || revoked != 11 {
		t.Fatal("incorrect saves or key lifecycle")
	}
}
func TestPhotoLinkFailures(t *testing.T) {
	calls := 0
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(413) }))
	token := mintPhoto(t, s, Caller{APIKey: "owner"})
	for i := 0; i < 11; i++ {
		if photoRequest(s, token, "POST").Code != 413 {
			t.Fatal("failed save consumed allowance")
		}
	}
	if calls != 11 {
		t.Fatal("failed saves")
	}
	hash := sha256.Sum256([]byte(token))
	entry := s.uploads[hash]
	entry.expires = time.Now().Add(-time.Second)
	s.uploads[hash] = entry
	if photoRequest(s, token, "GET").Code != 404 {
		t.Fatal("expired accepted")
	}
	token = mintPhoto(t, s, Caller{MintUpload: func(context.Context, bool) (string, func(), error) { return "", nil, errors.New("revoked") }})
	if photoRequest(s, token, "POST").Code != 403 || photoRequest(s, token, "GET").Code != 404 {
		t.Fatal("revoked sign-in accepted")
	}
	archive := mintUpload(t, s)
	if photoRequest(s, archive, "POST").Code != 404 {
		t.Fatal("archive accepted as photo")
	}
	for _, name := range []string{"../other", "blog/photos/../../me", "blog%2fphotos", "blog?site=other"} {
		if _, err := createUploadLink(&call{server: s}, map[string]any{"site": name}); err == nil {
			t.Fatal("unsafe site accepted")
		}
	}
	if _, err := createUploadLink(&call{server: s}, map[string]any{"site": "blog", "archive": "true"}); err == nil {
		t.Fatal("nonboolean archive accepted")
	}
}
func TestPhotoLinkConcurrentSave(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(200) }))
	token := mintPhoto(t, s, Caller{APIKey: "owner"})
	done := make(chan struct{})
	go func() { defer close(done); photoRequest(s, token, "POST") }()
	<-entered
	code := photoRequest(s, token, "POST").Code
	close(release)
	<-done
	if code != 409 {
		t.Fatal("concurrent save accepted")
	}
}
func TestHackPhotoToolRemainsArchive(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	for _, mode := range []string{CallerModeHackSite, CallerModeEvents} {
		caller := Caller{Mode: mode, GrantID: "grant", APIKey: "owner"}
		_, _, tools := s.forCaller(caller)
		if tools["create_upload_link"].Description != archiveUploadDescription {
			t.Fatal("Hack description changed")
		}
		out, err := createUploadLink(&call{server: s, caller: caller}, map[string]any{"site": "blog"})
		if err != nil || out.Structured["upload_url"] == nil || out.Structured["photos_url"] != nil || len(out.Structured) != 4 {
			t.Fatal("Hack default changed")
		}
	}
}

func TestPhotoLinkDoesNotExposeAccountQuota(t *testing.T) {
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Account-Data", "private")
		w.WriteHeader(413)
		w.Write([]byte(`{"code":"account_storage_full","error":"Account uses 999 MB","hint":"GET /v1/me","other_sites":["private"]}`))
	}))
	token := mintPhoto(t, s, Caller{APIKey: "owner"})
	w := photoRequest(s, token, "POST")
	if w.Code != 413 || strings.Contains(w.Body.String(), "999") || strings.Contains(w.Body.String(), "/v1/me") || strings.Contains(w.Body.String(), "private") || w.Header().Get("X-Account-Data") != "" {
		t.Fatal("photo error exposed account data")
	}
}

func TestPhotoLinkRetryAfter(t *testing.T) {
	s := NewServer(Config{APIHost: "box.example.org", Upstream: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":"Busy, try again in a moment"}`))
	})})
	token := mintPhoto(t, s, Caller{APIKey: "owner"})
	w := photoRequest(s, token, "POST")
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" || !strings.Contains(w.Body.String(), "Busy, try again in a moment") {
		t.Fatal("retry response lost")
	}
}
