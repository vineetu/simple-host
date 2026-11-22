package connector

// Upload and photo links (create_upload_link, PUT /v1/uploads/{token},
// /v1/photo-links) are the connector's: a link carries no key, works for one
// site, and ends with the sign-in it was made from. Sites' routes it forwards
// to are the fixture's stand-ins, which answer who the forwarded key acts as.

import (
	"net/url"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
)

// mintLink calls create_upload_link for site with an OAuth token (or, with
// key set, with an API key on /mcp) and returns the link's token and expiry.
func mintLink(t *testing.T, a *App, token, key, site string, archive bool) (string, time.Time) {
	t.Helper()
	params := map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": site, "archive": archive}}
	var r Resp
	if key != "" {
		r = a.Do(t, "POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params},
			map[string]string{"X-API-Key": key, "Content-Type": "application/json"})
	} else {
		r = a.Rpc(t, token, "tools/call", params)
	}
	_, out, failed := ToolResultOf(t, r)
	if failed {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	field := "photos_url"
	if archive {
		field = "upload_url"
	}
	u, err := url.Parse(out[field].(string))
	if err != nil {
		t.Fatal(err)
	}
	expires, err := time.Parse(time.RFC3339, out["expires_at"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if archive {
		return strings.TrimPrefix(u.Path, "/v1/uploads/"), expires
	}
	return strings.TrimPrefix(u.Fragment, "t="), expires
}

func putUpload(t *testing.T, a *App, token string) Resp {
	t.Helper()
	return a.Do(t, "PUT", "/v1/uploads/"+token, "archive", map[string]string{"Content-Type": "application/gzip"})
}

func photoLink(t *testing.T, a *App, method, token, query string) Resp {
	t.Helper()
	var body any
	if method == "POST" {
		body = "photos"
	}
	return a.Do(t, method, "/v1/photo-links"+query, body, map[string]string{"X-Photo-Token": token, "Origin": a.Srv.URL, "Content-Type": "multipart/form-data; boundary=x"})
}

func first(s string, _ time.Time) string { return s }

// A photo link shows only its site, saves to that site alone as its maker
// (no query can move it), and is spent after ten saves.
func TestPhotoLinkFixedSiteAndSpent(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "photo-link")
	token := a.Connect(t, owner, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	link, expires := mintLink(t, a, token, "", "blog", false)
	if left := time.Until(expires); left > time.Hour || left < 59*time.Minute {
		t.Fatalf("photo link lifetime: %v", left)
	}
	if r := photoLink(t, a, "GET", link, ""); r.Status != 200 || len(r.JSON(t)) != 1 || r.JSON(t)["site"] != "blog" {
		t.Fatalf("lookup: %d %s", r.Status, r.Body)
	}
	for i := 0; i < 10; i++ {
		r := photoLink(t, a, "POST", link, "?site=other&path=/v1/me")
		if r.Status != 200 || r.JSON(t)["site"] != "blog" || r.JSON(t)["user"] != owner.ID {
			t.Fatalf("save %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := photoLink(t, a, "POST", link, ""); r.Status != 404 || r.Code() != "photo_link_expired" {
		t.Fatalf("eleventh save: %d %s", r.Status, r.Body)
	}
	if r := photoLink(t, a, "GET", "not-a-link", ""); r.Status != 404 {
		t.Fatalf("unknown token: %d %s", r.Status, r.Body)
	}
}

// A photo link made with an API key ends when that key expires.
func TestPhotoLinkEndsWithKeyExpiry(t *testing.T) {
	a := NewApp(t)
	owner := a.NewPerson(t, "photo-expiry")
	expiry := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at=$1 WHERE key_hash=$2`, expiry, accountsrepo.HashAPIKey(owner.Key)); err != nil {
		t.Fatal(err)
	}
	if _, got := mintLink(t, a, "", owner.Key, "blog", false); !got.Equal(expiry) {
		t.Fatalf("API-key photo link expires %v, key %v", got, expiry)
	}
}
