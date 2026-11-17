package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCollectionReadHeadersAcrossDecisions protects the headers written before
// suspension and offline checks, private authorization, and author-visible reads.
func TestCollectionReadHeadersAcrossDecisions(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "board")
	a.sites.readLimiter = newRateLimiter(100, 100)
	siteID := a.siteID(t, olive, "board")
	owner := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}
	const apex = "simple-host.test"
	const domain = "olive-read.simple-host.test"
	const path = "/v1/sites/board/collections/orders"

	pageOrigin := "https://" + pcContentHost
	if r := a.at(t, "GET", apex, "/v1/sites/board/collections/bad.name", nil, map[string]string{"Origin": pageOrigin}); r.status != http.StatusBadRequest || r.header.Get("Access-Control-Allow-Origin") != "" || r.header.Get("RateLimit-Limit") != "" {
		t.Fatalf("invalid target: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
	if r := a.at(t, "GET", apex, path, nil, map[string]string{"Origin": pageOrigin}); r.status != http.StatusOK || r.header.Get("Access-Control-Allow-Origin") != pageOrigin || r.header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed-origin list: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
	if r := a.at(t, "GET", apex, path, nil, map[string]string{"Origin": "https://evil.example"}); r.status != http.StatusForbidden || r.json(t)["code"] != "origin_not_allowed" || r.header.Get("RateLimit-Limit") != "" {
		t.Fatalf("refused-origin list: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+siteID+"/suspend", map[string]string{"reason": "test"}, admin); r.status != http.StatusOK {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, path, nil, nil); r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" || r.header.Get("RateLimit-Limit") == "" {
		t.Fatalf("suspended list: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+siteID+"/restore", nil, admin); r.status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, "/v1/sites/board", map[string]bool{"offline": true}, owner); r.status != http.StatusOK {
		t.Fatalf("take offline: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, path, nil, nil); r.status != http.StatusForbidden || r.json(t)["code"] != "site_offline" || r.header.Get("RateLimit-Limit") == "" {
		t.Fatalf("offline list: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
	if r := a.at(t, "PATCH", apex, "/v1/sites/board", map[string]bool{"offline": false}, owner); r.status != http.StatusOK {
		t.Fatalf("bring online: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/board/domain", map[string]string{"domain": domain}, owner); r.status != http.StatusOK {
		t.Fatalf("domain: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", apex, path+"/privacy", map[string]bool{"private": true}, owner); r.status != http.StatusOK {
		t.Fatalf("private: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", domain, path, nil, nil); r.status != http.StatusNotFound || r.header.Get("Cache-Control") != "private, no-store" || r.header.Get("RateLimit-Limit") == "" {
		t.Fatalf("private refusal: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
	r := a.at(t, "GET", domain, path, nil, owner)
	vary := strings.Join(r.header.Values("Vary"), ",")
	if r.status != http.StatusOK || r.header.Get("Cache-Control") != "private, no-store" || !strings.Contains(vary, "Cookie") || !strings.Contains(vary, "X-API-Key") {
		t.Fatalf("owner view: status=%d body=%s headers=%v", r.status, r.body, r.header)
	}
}

// TestLegacyBodyLimitClosesConnection checks the transport behavior of deferred limit handling.
func TestLegacyBodyLimitClosesConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			_, _ = io.ReadAll(http.MaxBytesReader(w, r.Body, 2))
		} else {
			_, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 2))
			if err != nil {
				signalLegacyBodyTooLarge(w)
			}
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	}))
	defer server.Close()
	for _, path := range []string{"/old", "/new"} {
		resp, err := http.Post(server.URL+path, "application/json", strings.NewReader("123"))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge || !resp.Close {
			t.Fatalf("%s: status=%d close=%v headers=%v", path, resp.StatusCode, resp.Close, resp.Header)
		}
	}
}
