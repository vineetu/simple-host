package handler

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

func TestPhotoStalledBodyReleasesSlot(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "photo-deadline")
	if r := a.do(t, "POST", "/v1/sites/blog/files", jsonBody(map[string]any{"files": map[string]any{"index.html": "keep"}}), map[string]string{"X-API-Key": owner.key}); r.status != 201 {
		t.Fatal("create site failed")
	}
	_, out, failed := toolResultOf(t, a.do(t, "POST", "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog"}}}), map[string]string{"X-API-Key": owner.key}))
	if failed {
		t.Fatal("mint failed")
	}
	link, _ := url.Parse(out["photos_url"].(string))
	token := strings.TrimPrefix(link.Fragment, "t=")
	t.Setenv("PHOTO_MAX_CONCURRENT", "1")
	t.Setenv("PHOTO_BODY_TIMEOUT_SECONDS", "7")
	for _, tokenRoute := range []bool{false, true} {
		t.Run(fmt.Sprint(tokenRoute), func(t *testing.T) {
			set := make(chan time.Time, 2)
			done := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Preserve the connector's apex Host check, with the real metrics/notice wrappers.
				r.Host = strings.TrimPrefix(a.srv.URL, "http://")
				a.srv.Config.Handler.ServeHTTP(&statusCapture{ResponseWriter: shortImportDeadline{w, set}, status: 200}, r)
				close(done)
			}))
			defer server.Close()
			conn, err := net.Dial("tcp", server.Listener.Addr().String())
			if err != nil {
				t.Fatal("dial failed")
			}
			defer conn.Close()
			path, header, credential := "/v1/sites/blog/photos", "X-API-Key", owner.key
			if tokenRoute {
				path, header, credential = "/v1/photo-links", "X-Photo-Token", token
			}
			_, err = fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: test\r\n%s: %s\r\nX-Skill-Version: 0.0.1\r\nContent-Type: multipart/form-data; boundary=stalled\r\nContent-Length: 1000\r\n\r\n--stalled\r\n", path, header, credential)
			if err != nil {
				t.Fatal("write failed")
			}
			select {
			case deadline := <-set:
				if left := time.Until(deadline); left < 6*time.Second || left > 7*time.Second {
					t.Fatal("wrong body deadline")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("deadline not set on real writer")
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("stalled body retained slot")
			}
			if photoSaves.Load() != 0 {
				t.Fatal("photo slot leaked")
			}
			if tokenRoute {
				if r := a.do(t, "GET", "/v1/photo-links", nil, map[string]string{"X-Photo-Token": token}); r.status != 200 {
					t.Fatal("timed-out link cannot retry")
				}
			}
		})
	}
}

func TestConnectorPhotoCredentialExpiry(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "photo-expiry")
	client := a.registerClient(t, testRedirect)
	access := a.connect(t, owner, client, testRedirect)["access_token"].(string)
	expiry := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	if _, err := a.database.Exec(`UPDATE oauth_tokens SET expires_at=$1 WHERE token_hash=$2`, expiry, hashSecret(access)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE api_keys SET expires_at=$1 WHERE key_hash=$2`, expiry, db.HashAPIKey(owner.key)); err != nil {
		t.Fatal(err)
	}
	for _, oauth := range []bool{false, true} {
		var r resp
		params := map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog"}}
		if oauth {
			r = a.rpc(t, access, "tools/call", params)
		} else {
			r = a.do(t, "POST", "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}), map[string]string{"X-API-Key": owner.key})
		}
		text, out, failed := toolResultOf(t, r)
		if failed {
			t.Fatal("photo mint failed")
		}
		got, err := time.Parse(time.RFC3339, out["expires_at"].(string))
		if err != nil || !strings.Contains(text, got.Format(time.RFC3339)) {
			t.Fatal("photo result did not report expiry")
		}
		if oauth {
			if left := time.Until(got); left < 59*time.Minute || left > time.Hour {
				t.Fatal("OAuth photo lifetime is not one hour")
			}
		} else if !got.Equal(expiry) {
			t.Fatal("API-key photo lifetime exceeded key expiry")
		}
	}
}
